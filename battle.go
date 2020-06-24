package main

import (
	"github.com/pkg/errors"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/items"
	"github.com/NOVAPokemon/utils/pokemons"
	ws "github.com/NOVAPokemon/utils/websockets"
	"github.com/NOVAPokemon/utils/websockets/battles"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

type (
	Battle struct {
		Lobby               *ws.Lobby
		Winner              string
		cooldown            time.Duration
		RejectChannel       chan struct{}
		AuthTokens          [2]string
		PlayersBattleStatus [2]*battles.TrainerBattleStatus
		Expected            [2]string
	}
)

var cooldown int

func NewBattle(lobby *ws.Lobby, cooldown int, expected [2]string) *Battle {
	return &Battle{
		AuthTokens:          [2]string{},
		PlayersBattleStatus: [2]*battles.TrainerBattleStatus{},
		RejectChannel:       make(chan struct{}),
		Winner:              "",
		Lobby:               lobby,
		cooldown:            time.Duration(cooldown) + time.Millisecond,
		Expected:            expected,
	}
}

func (b *Battle) addPlayer(username string, pokemons map[string]*pokemons.Pokemon, stats *utils.TrainerStats,
	trainerItems map[string]items.Item, trainerConn *websocket.Conn, authToken string) (int, error) {

	trainersJoined, err := ws.AddTrainer(b.Lobby, username, trainerConn)

	if err != nil {
		return -1, wrapAddPlayerError(err)
	}

	player := &battles.TrainerBattleStatus{
		Username:        username,
		TrainerStats:    stats,
		TrainerPokemons: pokemons,
		TrainerItems:    trainerItems,
		CdTimer:         time.NewTimer(time.Duration(cooldown) * time.Millisecond),
		AllPokemonsDead: false,
		UsedItems:       make(map[string]items.Item),
	}

	b.PlayersBattleStatus[trainersJoined-1] = player
	b.AuthTokens[trainersJoined-1] = authToken
	return trainersJoined, nil
}

func (b *Battle) StartBattle() (string, error) {
	ws.StartLobby(b.Lobby)
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
	startMsg := ws.StartMessage{}.SerializeToWSMessage().Serialize()

	b.Lobby.TrainerOutChannels[0] <- ws.GenericMsg{
		MsgType: websocket.TextMessage,
		Data:    []byte(startMsg),
	}

	b.Lobby.TrainerOutChannels[1] <- ws.GenericMsg{
		MsgType: websocket.TextMessage,
		Data:    []byte(startMsg),
	}

	log.Info("Sent START message")
	// loops until both players have selected a pokemon
	for ; players[0].SelectedPokemon == nil || players[1].SelectedPokemon == nil; {
		b.logBattleStatus()
		select {
		case msgStr, ok := <-b.Lobby.TrainerInChannels[0]:
			if ok {
				b.handleMoveInSelectionPhase(msgStr, b.PlayersBattleStatus[0], b.Lobby.TrainerOutChannels[0])
			}
		case msgStr, ok := <-b.Lobby.TrainerInChannels[1]:
			if ok {
				b.handleMoveInSelectionPhase(msgStr, b.PlayersBattleStatus[1], b.Lobby.TrainerOutChannels[1])
			}
		case <-b.Lobby.DoneListeningFromConn[0]:
			err := newUserError(b.PlayersBattleStatus[0].Username)
			return wrapMainLoopError(err)
		case <-b.Lobby.DoneListeningFromConn[1]:
			err := newUserError(b.PlayersBattleStatus[1].Username)
			return wrapMainLoopError(err)
		case <-b.Lobby.DoneWritingToConn[0]:
			err := newUserError(b.PlayersBattleStatus[0].Username)
			return wrapMainLoopError(err)
		case <-b.Lobby.DoneWritingToConn[1]:
			err := newUserError(b.PlayersBattleStatus[1].Username)
			return wrapMainLoopError(err)
		}
	}

	log.Info("Battle setup finished")
	return nil
}

func (b *Battle) mainLoop() (string, error) {
	// main battle loop
	for {
		select {
		case msgStr, ok := <-b.Lobby.TrainerInChannels[0]:
			if ok {
				b.logBattleStatus()
				if battleFinished := b.handlePlayerMessage(msgStr, b.PlayersBattleStatus[0], b.PlayersBattleStatus[1],
					b.Lobby.TrainerOutChannels[0], b.Lobby.TrainerOutChannels[1]); battleFinished {
					return b.Winner, nil
				}
			}
		case msgStr, ok := <-b.Lobby.TrainerInChannels[1]:
			if ok {
				b.logBattleStatus()
				if battleFinished := b.handlePlayerMessage(msgStr, b.PlayersBattleStatus[1], b.PlayersBattleStatus[0],
					b.Lobby.TrainerOutChannels[1], b.Lobby.TrainerOutChannels[0]); battleFinished {
					return b.Winner, nil
				}
			}
		case <-b.PlayersBattleStatus[0].CdTimer.C:
			b.PlayersBattleStatus[0].Cooldown = false
			b.PlayersBattleStatus[0].Defending = false
			log.Warn("Removed player 0 Cooldown status")
		case <-b.PlayersBattleStatus[1].CdTimer.C:
			b.PlayersBattleStatus[1].Cooldown = false
			b.PlayersBattleStatus[1].Defending = false
			log.Warn("Removed player 1 Cooldown status")
		case <-b.Lobby.DoneListeningFromConn[0]:
			err := newUserError(b.PlayersBattleStatus[0].Username)
			return "", wrapMainLoopError(err)
		case <-b.Lobby.DoneListeningFromConn[1]:
			err := newUserError(b.PlayersBattleStatus[1].Username)
			return "", wrapMainLoopError(err)
		case <-b.Lobby.DoneWritingToConn[0]:
			err := newUserError(b.PlayersBattleStatus[0].Username)
			return "", wrapMainLoopError(err)
		case <-b.Lobby.DoneWritingToConn[1]:
			err := newUserError(b.PlayersBattleStatus[1].Username)
			return "", wrapMainLoopError(err)
		case <-b.Lobby.Finished:
			return "", wrapMainLoopError(errors.New("Lobby was finished before battle ended"))
		}
	}
}

func (b *Battle) handleMoveInSelectionPhase(msgStr string, issuer *battles.TrainerBattleStatus, issuerChan chan ws.GenericMsg) {
	message, err := ws.ParseMessage(msgStr)
	if err != nil {
		log.Error(err)
		issuerChan <- ws.GenericMsg{
			MsgType: websocket.TextMessage,
			Data: []byte(ws.ErrorMessage{
				Info:  ws.ErrorInvalidMessageFormat.Error(),
				Fatal: false,
			}.SerializeToWSMessage().Serialize())}
		return
	}

	if message.MsgType != battles.SelectPokemon {
		issuerChan <- ws.GenericMsg{
			MsgType: websocket.TextMessage,
			Data: []byte(ws.ErrorMessage{
				Info:  battles.ErrorPokemonSelectionPhase.Error(),
				Fatal: false,
			}.SerializeToWSMessage().Serialize())}
		return
	}

	desMsg, err := battles.DeserializeBattleMsg(message)
	if err != nil {
		log.Error(err)
		return
	}

	selectPokemonMsg := desMsg.(*battles.SelectPokemonMessage)
	battles.HandleSelectPokemon(selectPokemonMsg, issuer, issuerChan)
}

// handles the reception of a move from a player.
func (b *Battle) handlePlayerMessage(msgStr string, issuer, otherPlayer *battles.TrainerBattleStatus,
	issuerChan, otherPlayerChan chan ws.GenericMsg) (battleFinished bool) {
	message, err := ws.ParseMessage(msgStr)
	if err != nil {
		log.Error(err)
		issuerChan <- ws.GenericMsg{
			MsgType: websocket.TextMessage,
			Data: []byte(ws.ErrorMessage{
				Info:  ws.ErrorInvalidMessageType.Error(),
				Fatal: false,
			}.SerializeToWSMessage().Serialize())}
		return false
	}
	switch message.MsgType {
	case battles.Attack:
		if changed := battles.HandleAttackMove(issuer, issuerChan, otherPlayer.Defending, otherPlayer.SelectedPokemon, b.cooldown); changed {
			desMsg, err := battles.DeserializeBattleMsg(message)
			if err != nil {
				log.Error(err)
				return false
			}
			attackMsg := desMsg.(*battles.AttackMessage)
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
				b.Winner = issuer.Username
				return true
			}

			battles.UpdateTrainerPokemon(attackMsg.TrackedMessage, *otherPlayer.SelectedPokemon, otherPlayerChan, true)
			battles.UpdateTrainerPokemon(attackMsg.TrackedMessage, *otherPlayer.SelectedPokemon, issuerChan, false)
		}
	case battles.Defend:
		battles.HandleDefendMove(issuer, issuerChan, b.cooldown)
		issuerChan <- ws.GenericMsg{
			MsgType: websocket.TextMessage,
			Data: []byte(battles.StatusMessage{
				Message: "Enemy is defending",
			}.SerializeToWSMessage().Serialize()),
		}
	case battles.UseItem:
		desMsg, err := battles.DeserializeBattleMsg(message)
		if err != nil {
			log.Error(err)
			return false
		}

		useItemMsg := desMsg.(*battles.UseItemMessage)
		if changed := battles.HandleUseItem(useItemMsg, issuer, issuerChan, b.cooldown); changed {
			battles.UpdateTrainerPokemon(useItemMsg.TrackedMessage, *issuer.SelectedPokemon, otherPlayerChan, false)
		}
	case battles.SelectPokemon:
		desMsg, err := battles.DeserializeBattleMsg(message)
		if err != nil {
			log.Error(err)
			return false
		}

		selectPokemonMsg := desMsg.(*battles.SelectPokemonMessage)
		if changed := battles.HandleSelectPokemon(selectPokemonMsg, issuer, issuerChan); changed {
			battles.UpdateTrainerPokemon(selectPokemonMsg.TrackedMessage, *issuer.SelectedPokemon, otherPlayerChan, false)
		}
	default:
		log.Error(ws.NewInvalidMsgTypeError(message.MsgType))
		issuerChan <- ws.GenericMsg{
			MsgType: websocket.TextMessage,
			Data: []byte(ws.ErrorMessage{
				Info:  ws.ErrorInvalidMessageType.Error(),
				Fatal: false,
			}.SerializeToWSMessage().Serialize()),
		}
	}
	return false
}

func (b *Battle) FinishBattle() {
	toSend := ws.GenericMsg{
		MsgType: websocket.TextMessage,
		Data:    []byte(ws.FinishMessage{}.SerializeToWSMessage().Serialize()),
	}
	b.Lobby.TrainerOutChannels[0] <- toSend
	b.Lobby.TrainerOutChannels[1] <- toSend
	wg := sync.WaitGroup{}
	for i := 0; i < ws.GetTrainersJoined(b.Lobby); i++ {
		wg.Add(1)
		trainerNr := i
		go func() {
			defer wg.Done()
			select {
			case <-b.Lobby.DoneListeningFromConn[trainerNr]:
			case <-time.After(3 * time.Second):
			}
		}()
	}
	wg.Wait()
	ws.FinishLobby(b.Lobby)
}

func (b *Battle) logBattleStatus() {
	log.Info("----------------------------------------")
	pokemon := b.PlayersBattleStatus[0].SelectedPokemon
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
