package main

import (
	"fmt"
	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/items"
	"github.com/NOVAPokemon/utils/pokemons"
	ws "github.com/NOVAPokemon/utils/websockets"
	"github.com/NOVAPokemon/utils/websockets/battles"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
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
	}
)

func NewBattle(lobby *ws.Lobby) *Battle {
	return &Battle{
		AuthTokens:          [2]string{},
		PlayersBattleStatus: [2]*battles.TrainerBattleStatus{},
		Finished:            false,
		StartChannel:        make(chan struct{}),
		Winner:              "",
		Lobby:               lobby,
	}
}

func (b *Battle) addPlayer(username string, pokemons map[string]*pokemons.Pokemon, stats *utils.TrainerStats, trainerItems map[string]items.Item, trainerConn *websocket.Conn, playerNr int, authToken string) {

	player := &battles.TrainerBattleStatus{
		Username:        username,
		TrainerStats:    stats,
		TrainerPokemons: pokemons,
		TrainerItems:    trainerItems, CdTimer: time.NewTimer(battles.DefaultCooldown),
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
		log.Error(err)
		return "", nil
	}

	winner, err := b.mainLoop()

	if err != nil {
		log.Error(err)
		return "", nil
	}

	return winner, err
}

func (b *Battle) setupLoop() error {
	players := b.PlayersBattleStatus
	startMsg := ws.Message{MsgType: battles.Start, MsgArgs: []string{}}
	ws.SendMessage(startMsg, *b.Lobby.TrainerOutChannels[0])
	ws.SendMessage(startMsg, *b.Lobby.TrainerOutChannels[1])
	log.Info("Sent START message")

	// loops until both players have selected a pokemon
	for ; players[0].SelectedPokemon == nil || players[1].SelectedPokemon == nil; {
		b.logBattleStatus()
		select {

		case msgStr := <-*b.Lobby.TrainerInChannels[0]:
			if err := battles.HandleSelectPokemon(msgStr, players[0], *b.Lobby.TrainerOutChannels[0]); err != nil {
				battles.UpdateAdversaryOfPokemonChanges(* players[0].SelectedPokemon, *b.Lobby.TrainerOutChannels[1])
			}
		case msgStr := <-*b.Lobby.TrainerInChannels[1]:
			if err := battles.HandleSelectPokemon(msgStr, players[1], *b.Lobby.TrainerOutChannels[1]); err != nil {
				battles.UpdateAdversaryOfPokemonChanges(* players[1].SelectedPokemon, *b.Lobby.TrainerOutChannels[0])
			}
		case <-b.Lobby.EndConnectionChannels[0]:
			err := fmt.Sprintf("An error occurred with user %s", b.PlayersBattleStatus[0].Username)
			log.Error(err)
			ws.CloseLobby(b.Lobby)
			return errors.New(err)
		case <-b.Lobby.EndConnectionChannels[1]:
			err := fmt.Sprintf("An error occurred with user %s", b.PlayersBattleStatus[1].Username)
			log.Error(err)
			ws.CloseLobby(b.Lobby)
			return errors.New(err)
		}

	}

	log.Info("Battle setup finished")
	b.Selecting = false
	return nil
}

func (b *Battle) mainLoop() (string, error) {
	// main battle loop
	for ; !b.Finished; {
		b.logBattleStatus()
		select {
		case msgStr, ok := <-*b.Lobby.TrainerInChannels[0]:
			if ok {
				b.handlePlayerMove(msgStr, b.PlayersBattleStatus[0], *b.Lobby.TrainerOutChannels[0], b.PlayersBattleStatus[1], *b.Lobby.TrainerOutChannels[1])
			}
		case msgStr, ok := <-*b.Lobby.TrainerInChannels[1]:
			if ok {
				b.handlePlayerMove(msgStr, b.PlayersBattleStatus[1], *b.Lobby.TrainerOutChannels[1], b.PlayersBattleStatus[0], *b.Lobby.TrainerOutChannels[0])
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
			err := fmt.Sprintf("An error occurred with user %s", b.PlayersBattleStatus[0].Username)
			log.Error(err)
			ws.CloseLobby(b.Lobby)
			return "", errors.New(err)
		case <-b.Lobby.EndConnectionChannels[1]:
			err := fmt.Sprintf("An error occurred with user %s", b.PlayersBattleStatus[1].Username)
			log.Error(err)
			ws.CloseLobby(b.Lobby)
			return "", errors.New(err)
		}
	}
	return b.Winner, nil
}

// handles the reception of a move from a player.
func (b *Battle) handlePlayerMove(msgStr *string, issuer *battles.TrainerBattleStatus, issuerChan chan *string, otherPlayer *battles.TrainerBattleStatus, otherPlayerChan chan *string) {

	message, err := ws.ParseMessage(msgStr)
	if err != nil {
		errMsg := ws.Message{MsgType: battles.Error, MsgArgs: []string{battles.ErrInvalidMessageFormat.Error()}}
		ws.SendMessage(errMsg, issuerChan)
		return
	}
	switch message.MsgType {

	case battles.Attack:
		if changed, err := battles.HandleAttackMove(issuer, issuerChan, otherPlayer.Defending, otherPlayer.SelectedPokemon); changed && err == nil {
			if issuer.AllPokemonsDead {
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
			battles.UpdateTrainerPokemon(*otherPlayer.SelectedPokemon, otherPlayerChan)
			battles.UpdateAdversaryOfPokemonChanges(*otherPlayer.SelectedPokemon, otherPlayerChan)
		}
		break
	case battles.Defend:
		if err := battles.HandleDefendMove(issuer, issuerChan); err == nil {
			updateAdversaryOfDefendingMove(otherPlayerChan)
		}
		break

	case battles.UseItem:
		if err := battles.HandleUseItem(message, issuer, issuerChan); err == nil {
			battles.UpdateAdversaryOfPokemonChanges(*issuer.SelectedPokemon, otherPlayerChan)
		}
		break

	case battles.SelectPokemon:
		if err := battles.HandleSelectPokemon(msgStr, issuer, issuerChan); err == nil {
			battles.UpdateAdversaryOfPokemonChanges(*issuer.SelectedPokemon, otherPlayerChan)
		}
		break

	default:
		log.Errorf("cannot handle message type: %s ", message.MsgType)
		msg := ws.Message{MsgType: battles.Error, MsgArgs: []string{fmt.Sprintf(battles.ErrInvalidMessageType.Error())}}
		ws.SendMessage(msg, issuerChan)
		return
	}
}

func updateAdversaryOfDefendingMove(adversaryChan chan *string) {
	msg := ws.Message{MsgType: battles.AdversaryDefending, MsgArgs: []string{battles.DefaultCooldown.String()}}
	ws.SendMessage(msg, adversaryChan)
}

func (b *Battle) FinishBattle(winner string) {
	b.Lobby.Finished = true
	finishMsg := ws.Message{MsgType: battles.Finish, MsgArgs: []string{winner}}
	ws.SendMessage(finishMsg, *b.Lobby.TrainerOutChannels[0])
	ws.SendMessage(finishMsg, *b.Lobby.TrainerOutChannels[1])

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
		log.Infof("Player 0 pokemon: id: %s; Species : %s ; Damage: %d; HP: %d/%d;", pokemon.Id.Hex(), pokemon.Species, pokemon.Damage, pokemon.HP, pokemon.MaxHP)
	}

	pokemon = b.PlayersBattleStatus[1].SelectedPokemon
	log.Infof("Player 1 status: Defending:%t ; Cooldown:%t ", b.PlayersBattleStatus[1].Defending, b.PlayersBattleStatus[1].Cooldown)
	if pokemon != nil {
		log.Infof("Player 1 pokemon: id: %s; Species : %s ; Damage: %d; HP: %d/%d;", pokemon.Id.Hex(), pokemon.Species, pokemon.Damage, pokemon.HP, pokemon.MaxHP)
	}
}
