package main

import (
	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/items"
	"github.com/NOVAPokemon/utils/pokemons"
	ws "github.com/NOVAPokemon/utils/websockets"
	"github.com/NOVAPokemon/utils/websockets/battles"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
	"time"
)

type (
	Battle struct {
		Lobby               *ws.Lobby
		AuthTokens          [2]string
		PlayersBattleStatus [2]*battles.TrainerBattleStatus
		Winner              string
		StartChannel        chan struct{}
		Selecting           bool
		Finished            bool
		cooldown            time.Duration
	}
)

var cooldown int

func NewBattle(lobby *ws.Lobby, cooldown int) *Battle {
	return &Battle{
		AuthTokens:          [2]string{},
		PlayersBattleStatus: [2]*battles.TrainerBattleStatus{},
		Finished:            false,
		StartChannel:        make(chan struct{}),
		Winner:              "",
		Lobby:               lobby,
		cooldown:            time.Duration(cooldown) + time.Millisecond,
	}
}

func (b *Battle) addPlayer(username string, pokemons map[string]*pokemons.Pokemon, stats *utils.TrainerStats,
	trainerItems map[string]items.Item, trainerConn *websocket.Conn, playerNr int, authToken string) {

	player := &battles.TrainerBattleStatus{
		Username:        username,
		TrainerStats:    stats,
		TrainerPokemons: pokemons,
		TrainerItems:    trainerItems,
		CdTimer:         time.NewTimer(time.Duration(cooldown) * time.Millisecond),
		AllPokemonsDead: false,
		UsedItems:       make(map[string]items.Item),
	}

	ws.AddTrainer(b.Lobby, username, trainerConn)
	b.PlayersBattleStatus[playerNr] = player
	b.AuthTokens[playerNr] = authToken
}

func (b *Battle) StartBattle() (string, error) {
	close(b.StartChannel)
	b.Lobby.Started = true

	err := b.setupLoop()
	if err != nil {
		return "", wrapStartBattleError(err, b.Lobby.Id.Hex())
	}

	winner, err := b.mainLoop()
	if err != nil {
		return "", wrapStartBattleError(err, b.Lobby.Id.Hex())
	}

	return winner, nil
}

func (b *Battle) setupLoop() error {
	players := b.PlayersBattleStatus
	startMsg := battles.StartMessage{}.SerializeToWSMessage()
	ws.SendMessage(*startMsg, *b.Lobby.TrainerOutChannels[0])
	ws.SendMessage(*startMsg, *b.Lobby.TrainerOutChannels[1])
	log.Info("Sent START message")

	// loops until both players have selected a pokemon
	for ; players[0].SelectedPokemon == nil || players[1].SelectedPokemon == nil; {
		b.logBattleStatus()

		select {
		case msgStr := <-*b.Lobby.TrainerInChannels[0]:
			b.handleMoveInSelectionPhase(msgStr, b.PlayersBattleStatus[0], *b.Lobby.TrainerOutChannels[0],
				*b.Lobby.TrainerOutChannels[1])
		case msgStr := <-*b.Lobby.TrainerInChannels[1]:
			b.handleMoveInSelectionPhase(msgStr, b.PlayersBattleStatus[1],
				*b.Lobby.TrainerOutChannels[1], *b.Lobby.TrainerOutChannels[0])
		case <-b.Lobby.EndConnectionChannels[0]:
			err := newUserError(b.PlayersBattleStatus[0].Username)
			ws.CloseLobby(b.Lobby)
			return wrapSetupLoopError(err)
		case <-b.Lobby.EndConnectionChannels[1]:
			err := newUserError(b.PlayersBattleStatus[1].Username)
			ws.CloseLobby(b.Lobby)
			return wrapSetupLoopError(err)
		}
	}

	log.Info("Battle setup finished")
	b.Selecting = false
	return nil
}

func (b *Battle) mainLoop() (string, error) {
	// main battle loop
	for !b.Finished {
		b.logBattleStatus()
		select {
		case msgStr, ok := <-*b.Lobby.TrainerInChannels[0]:
			if ok {
				b.handlePlayerMessage(msgStr, b.PlayersBattleStatus[0], b.PlayersBattleStatus[1],
					*b.Lobby.TrainerOutChannels[0], *b.Lobby.TrainerOutChannels[1])
			}
		case msgStr, ok := <-*b.Lobby.TrainerInChannels[1]:
			if ok {
				b.handlePlayerMessage(msgStr, b.PlayersBattleStatus[1], b.PlayersBattleStatus[0],
					*b.Lobby.TrainerOutChannels[1], *b.Lobby.TrainerOutChannels[0])
			}
		case <-b.PlayersBattleStatus[0].CdTimer.C:
			b.PlayersBattleStatus[0].Cooldown = false
			b.PlayersBattleStatus[0].Defending = false
			log.Warn("Removed player 0 Cooldown status")
		case <-b.PlayersBattleStatus[1].CdTimer.C:
			b.PlayersBattleStatus[1].Cooldown = false
			b.PlayersBattleStatus[1].Defending = false
			log.Warn("Removed player 1 Cooldown status")
		case <-b.Lobby.EndConnectionChannels[0]:
			err := newUserError(b.PlayersBattleStatus[0].Username)
			return "", wrapMainLoopError(err)
		case <-b.Lobby.EndConnectionChannels[1]:
			err := newUserError(b.PlayersBattleStatus[1].Username)
			return "", wrapMainLoopError(err)
		}
	}
	return b.Winner, nil
}

func (b *Battle) handleMoveInSelectionPhase(msgStr *string, issuer *battles.TrainerBattleStatus,
	issuerChan, otherPlayerChan chan *string) {
	message, err := ws.ParseMessage(msgStr)
	if err != nil {
		log.Error(ws.WrapMsgParsingError(err, *msgStr))
		ws.SendMessage(
			*battles.ErrorMessage{
				Info:  ws.ErrorInvalidMessageFormat.Error(),
				Fatal: false,
			}.SerializeToWSMessage(), issuerChan)
		return
	}

	if message.MsgType != battles.SelectPokemon {
		ws.SendMessage(
			*battles.ErrorMessage{
				Info:  battles.ErrorPokemonSelectionPhase.Error(),
				Fatal: false,
			}.SerializeToWSMessage(), issuerChan)
		return
	}

	selectPokemonMsg := battles.DeserializeBattleMsg(message).(*battles.SelectPokemonMessage)
	battles.HandleSelectPokemon(selectPokemonMsg, issuer, issuerChan)
}

// handles the reception of a move from a player.
func (b *Battle) handlePlayerMessage(msgStr *string, issuer, otherPlayer *battles.TrainerBattleStatus,
	issuerChan, otherPlayerChan chan *string) {

	message, err := ws.ParseMessage(msgStr)
	if err != nil {
		log.Error(ws.WrapMsgParsingError(err, *msgStr))
		ws.SendMessage(
			*battles.ErrorMessage{
				Info:  ws.ErrorInvalidMessageType.Error(),
				Fatal: false,
			}.SerializeToWSMessage(),
			issuerChan)
		return
	}

	switch message.MsgType {

	case battles.Attack:
		if changed := battles.HandleAttackMove(issuer, issuerChan, otherPlayer.Defending, otherPlayer.SelectedPokemon, b.cooldown); changed {

			attackMsg := battles.DeserializeBattleMsg(message).(*battles.AttackMessage)

			if otherPlayer.SelectedPokemon.HP == 0 {
				allDead := true
				for _, pokemon := range otherPlayer.TrainerPokemons {
					if pokemon.HP > 0 {
						allDead = false
						break
					}
				}
				if allDead {
					otherPlayer.AllPokemonsDead = true
				}
			}

			if otherPlayer.AllPokemonsDead {
				// battle is finished

				log.Info("--------------BATTLE ENDED---------------")
				log.Infof("Winner : %s", issuer.Username)
				log.Infof("Trainer 0 (%s) pokemons:", issuer.Username)
				for _, v := range b.PlayersBattleStatus[0].TrainerPokemons {
					log.Infof("Pokemon %s:\t HP:%d", v.Id.Hex(), v.HP)
				}

				log.Infof("Trainer 1 (%s) pokemons:", issuer.Username)
				for _, v := range b.PlayersBattleStatus[1].TrainerPokemons {
					log.Infof("Pokemon %s:\t HP:%d", v.Id.Hex(), v.HP)
				}

				b.Finished = true
				b.Winner = issuer.Username
			}

			battles.UpdateTrainerPokemon(attackMsg.TrackedMessage, *otherPlayer.SelectedPokemon, otherPlayerChan, true)
			battles.UpdateTrainerPokemon(attackMsg.TrackedMessage, *otherPlayer.SelectedPokemon, issuerChan, false)
		}
		break
	case battles.Defend:
		battles.HandleDefendMove(issuer, issuerChan, b.cooldown)
		ws.SendMessage(
			*battles.StatusMessage{
				Message: "Enemy is defending",
			}.SerializeToWSMessage(), issuerChan)
	case battles.UseItem:
		useItemMsg := battles.DeserializeBattleMsg(message).(*battles.UseItemMessage)
		if changed := battles.HandleUseItem(useItemMsg, issuer, issuerChan, b.cooldown); changed {
			battles.UpdateTrainerPokemon(useItemMsg.TrackedMessage, *issuer.SelectedPokemon, otherPlayerChan, false)
		}
	case battles.SelectPokemon:
		selectPokemonMsg := battles.DeserializeBattleMsg(message).(*battles.SelectPokemonMessage)
		if changed := battles.HandleSelectPokemon(selectPokemonMsg, issuer, issuerChan); changed {
			battles.UpdateTrainerPokemon(selectPokemonMsg.TrackedMessage, *issuer.SelectedPokemon, otherPlayerChan, false)
		}
	default:
		log.Error(ws.NewInvalidMsgTypeError(message.MsgType))
		ws.SendMessage(
			*battles.ErrorMessage{
				Info:  ws.ErrorInvalidMessageType.Error(),
				Fatal: false,
			}.SerializeToWSMessage(), issuerChan)
	}
}

func (b *Battle) FinishBattle(winner string) {
	b.Lobby.Finished = true
	toSend := *battles.FinishMessage{}.SerializeToWSMessage()
	ws.SendMessage(toSend, *b.Lobby.TrainerOutChannels[0])
	ws.SendMessage(toSend, *b.Lobby.TrainerOutChannels[1])

	<-b.Lobby.EndConnectionChannels[0]
	<-b.Lobby.EndConnectionChannels[1]
	ws.CloseLobby(b.Lobby)
}

func (b *Battle) logBattleStatus() {
	log.Info("----------------------------------------")
	pokemon := b.PlayersBattleStatus[0].SelectedPokemon
	log.Infof("Battle %s Info: selecting:%t", b.Lobby.Id.Hex(), b.Selecting)
	log.Infof("Player 0 status: Defending:%t ; Cooldown:%t ", b.PlayersBattleStatus[0].Defending, b.PlayersBattleStatus[0].Cooldown)
	if pokemon != nil {
		log.Infof("Player 0 pokemon:ID:%s, Damage:%d, HP:%d, maxHP:%d, Species:%s", pokemon.Id.Hex(), pokemon.Damage, pokemon.HP, pokemon.MaxHP, pokemon.Species)
	}

	pokemon = b.PlayersBattleStatus[1].SelectedPokemon
	log.Infof("Player 1 status: Defending:%t ; Cooldown:%t ", b.PlayersBattleStatus[1].Defending, b.PlayersBattleStatus[1].Cooldown)
	if pokemon != nil {
		log.Infof("Player 1 pokemon:ID:%s, Damage:%d, HP:%d, maxHP:%d, Species:%s", pokemon.Id.Hex(), pokemon.Damage, pokemon.HP, pokemon.MaxHP, pokemon.Species)
	}
}
