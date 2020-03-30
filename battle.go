package main

import (
	"encoding/json"
	"fmt"
	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/websockets"
	"github.com/NOVAPokemon/utils/websockets/battles"
	log "github.com/sirupsen/logrus"
	"math/rand"
	"time"
)

const DefaultCooldown = time.Second * 2

type (
	BattleStatus struct {
		BattleStarted  bool
		BattleFinished bool
		Winner         *utils.Trainer
		Players        [2]*trainerStatus
	}

	trainerStatus struct {
		Trainer *utils.Trainer

		selectedPokemon *utils.Pokemon
		defending       bool
		cooldown        bool
		cdTimer         *time.Timer

		playerInChannel  chan *string
		playerOutChannel chan *string
	}
)

func StartBattle(lobby *websockets.Lobby) (*BattleStatus, error) {

	battleStatus, err := setupLoop(lobby)

	if err != nil {
		log.Error(err)
		return nil, err
	}

	err = mainLoop(battleStatus)

	if err != nil {
		log.Error(err)
		return nil, err
	}

	return battleStatus, nil
}

func setupLoop(lobby *websockets.Lobby) (*BattleStatus, error) {

	// TODO timer to break the loop if a player takes too long to select

	players := [2]*trainerStatus{
		{
			lobby.Trainers[0], nil,
			false, false, time.NewTimer(DefaultCooldown),
			*lobby.TrainerInChannels[0], *lobby.TrainerOutChannels[0],
		},
		{
			lobby.Trainers[1], nil,
			false, false, time.NewTimer(DefaultCooldown),
			*lobby.TrainerInChannels[1], *lobby.TrainerOutChannels[1],
		},
	}

	// loops until both players have selected a pokemon
	for ; players[0].selectedPokemon == nil || players[1].selectedPokemon == nil; {
		select {

		case msgStr := <-players[0].playerInChannel:

			err, msg := battles.ParseMessage(msgStr)

			if err != nil {
				errMsg := &battles.BattleMessage{MsgType: battles.ERROR, MsgArgs: []string{"Invalid message type"}}
				battles.SendMessage(errMsg, players[0].playerOutChannel)
				break
			}

			if msg.MsgType != battles.SELECT_POKEMON {
				errMsg := &battles.BattleMessage{MsgType: battles.ERROR, MsgArgs: []string{"Wrong message type, needs: SELECT_POKEMON"}}
				battles.SendMessage(errMsg, players[0].playerOutChannel)
				break
			}

			handleSelectPokemon(msg, players[0], players[1])

		case msgStr := <-players[1].playerInChannel:

			err, msg := battles.ParseMessage(msgStr)

			if err != nil {
				errMsg := &battles.BattleMessage{MsgType: battles.ERROR, MsgArgs: []string{"Invalid message type"}}
				battles.SendMessage(errMsg, players[0].playerOutChannel)
				break
			}

			if msg.MsgType != battles.SELECT_POKEMON {
				errMsg := &battles.BattleMessage{MsgType: battles.ERROR, MsgArgs: []string{"Wrong message type, needs: SELECT_POKEMON"}}
				battles.SendMessage(errMsg, players[0].playerOutChannel)
				break
			}

			handleSelectPokemon(msg, players[1], players[0])
		}
	}

	log.Infof("Battle setup finished")
	return &BattleStatus{true, false, nil, players}, nil
}

func mainLoop(battleStatus *BattleStatus) error {

	go handlePlayerCooldownTimer(battleStatus, battleStatus.Players[0])
	go handlePlayerCooldownTimer(battleStatus, battleStatus.Players[1])

	// main battle loop
	for ; !battleStatus.BattleFinished; {
		select {

		case msgStr := <-battleStatus.Players[0].playerInChannel:

			err, msg := battles.ParseMessage(msgStr)
			if err != nil {
				errMsg := &battles.BattleMessage{MsgType: battles.ERROR, MsgArgs: []string{"Invalid message format"}}
				battles.SendMessage(errMsg, battleStatus.Players[0].playerOutChannel)
			}

			handlePlayerMove(battleStatus, msg, battleStatus.Players[0], battleStatus.Players[1])

		case msgStr := <-battleStatus.Players[1].playerInChannel:

			err, msg := battles.ParseMessage(msgStr)
			if err != nil {
				errMsg := &battles.BattleMessage{MsgType: battles.ERROR, MsgArgs: []string{"Invalid message format"}}
				battles.SendMessage(errMsg, battleStatus.Players[0].playerOutChannel)
			}

			handlePlayerMove(battleStatus, msg, battleStatus.Players[1], battleStatus.Players[0])
		}
	}

	return nil
}

// handles the reception of a move from a player.
func handlePlayerMove(battleStatus *BattleStatus, message *battles.BattleMessage, issuer *trainerStatus, otherPlayer *trainerStatus) {

	switch message.MsgType {

	case battles.ATTACK:
		handleAttackMove(battleStatus, issuer, otherPlayer)

	case battles.DEFEND:
		handleDefendMove(battleStatus, issuer, otherPlayer)

	case battles.USE_ITEM:
		//TODO

	case battles.SELECT_POKEMON:
		handleSelectPokemon(message, issuer, otherPlayer)

	default:
		msg := &battles.BattleMessage{MsgType: battles.ERROR, MsgArgs: []string{fmt.Sprintf("Unknown command")}}
		battles.SendMessage(msg, issuer.playerOutChannel)
		return
	}
}

// handles the reception of a SELECT_POKEMON message, sends error message if message is not of type SELECT_POKEMON
func handleSelectPokemon(message *battles.BattleMessage, issuer *trainerStatus, otherPlayer *trainerStatus) {

	if len(message.MsgArgs) < 1 {
		errMsg := &battles.BattleMessage{MsgType: battles.ERROR, MsgArgs: []string{"no pokemon ID supplied"}}
		battles.SendMessage(errMsg, issuer.playerOutChannel)
		return
	}

	selectedPokemon := message.MsgArgs[0]

	for _, pokemon := range issuer.Trainer.Pokemons {
		if pokemon.Id.Hex() == selectedPokemon {

			if pokemon.HP <= 0 {
				// pokemon is dead
				msg := &battles.BattleMessage{MsgType: battles.ERROR, MsgArgs: []string{fmt.Sprintf("The selected pokemon has no HP")}}
				battles.SendMessage(msg, issuer.playerOutChannel)

			}

			issuer.selectedPokemon = &pokemon

			log.Infof("player 0 selected pokemon %+v", pokemon)
			toSend, err := json.Marshal(pokemon)

			if err != nil {
				log.Error(err)

			}

			log.Infof("%s", toSend)

			msg := &battles.BattleMessage{MsgType: battles.UPDATE_ADVERSARY_POKEMON, MsgArgs: []string{string(toSend)}}
			battles.SendMessage(msg, otherPlayer.playerOutChannel)
			msg = &battles.BattleMessage{MsgType: battles.UPDATE_PLAYER_POKEMON, MsgArgs: []string{string(toSend)}}
			battles.SendMessage(msg, issuer.playerOutChannel)
			return
		}
	}

	errMsg := &battles.BattleMessage{MsgType: battles.ERROR, MsgArgs: []string{"You do not own that Pokemon"}}
	battles.SendMessage(errMsg, issuer.playerOutChannel)
}

func handleDefendMove(battleStatus *BattleStatus, issuer *trainerStatus, otherPlayer *trainerStatus) {

	// if the pokemon is dead, player must select a new pokemon
	if issuer.selectedPokemon.HP == 0 {
		errMsg := &battles.BattleMessage{MsgType: battles.ERROR, MsgArgs: []string{"Your Pokemon is dead, select a new one"}}
		battles.SendMessage(errMsg, issuer.playerOutChannel)
		return
	}

	// if player has moved recently and is in cooldown, discard move
	if issuer.cooldown {
		errMsg := &battles.BattleMessage{MsgType: battles.ERROR, MsgArgs: []string{"You are in cooldown"}}
		battles.SendMessage(errMsg, issuer.playerOutChannel)
		return
	}
	issuer.cdTimer.Reset(DefaultCooldown)
	issuer.cooldown = true

	// process defending move: update both players and setup a cooldown
	issuer.defending = true
	toSend, err := json.Marshal(issuer)

	if err != nil {
		log.Error(err)
		return
	}

	msg := &battles.BattleMessage{MsgType: battles.UPDATE_PLAYER, MsgArgs: []string{string(toSend)}}
	battles.SendMessage(msg, issuer.playerOutChannel)

	msg = &battles.BattleMessage{MsgType: battles.UPDATE_ADVERSARY, MsgArgs: []string{string(toSend)}}
	battles.SendMessage(msg, otherPlayer.playerOutChannel)

	go func() { // after the player cooldown expires, remove defending status
		<-issuer.cdTimer.C
		issuer.defending = false
	}()
}

func handleAttackMove(battleStatus *BattleStatus, issuer *trainerStatus, otherPlayer *trainerStatus) {

	if issuer.selectedPokemon.HP == 0 {
		errMsg := &battles.BattleMessage{MsgType: battles.ERROR, MsgArgs: []string{"Your Pokemon is dead, select a new one"}}
		battles.SendMessage(errMsg, issuer.playerOutChannel)
		return
	}

	// if player has moved recently and is in cooldown, discard move
	if issuer.cooldown {
		errMsg := &battles.BattleMessage{MsgType: battles.ERROR, MsgArgs: []string{"You are in cooldown"}}
		battles.SendMessage(errMsg, issuer.playerOutChannel)
		return
	}
	issuer.cdTimer.Reset(DefaultCooldown)
	issuer.cooldown = true

	if otherPlayer.defending {

		msg := &battles.BattleMessage{MsgType: battles.STATUS, MsgArgs: []string{"Attack defended by opponent"}}
		battles.SendMessage(msg, issuer.playerOutChannel)

		msg = &battles.BattleMessage{MsgType: battles.STATUS, MsgArgs: []string{"Defended an attack by the opponent"}}
		battles.SendMessage(msg, issuer.playerOutChannel)
		return

	} else {

		otherPlayer.selectedPokemon.HP -= int(float32(issuer.selectedPokemon.Damage) * rand.Float32())

		if otherPlayer.selectedPokemon.HP < 0 {
			otherPlayer.selectedPokemon.HP = 0

			allPokemonsDead := true
			for _, pokemon := range otherPlayer.Trainer.Pokemons {
				if pokemon.HP > 0 {
					allPokemonsDead = false
					break
				}
			}

			if allPokemonsDead {
				// battle is finished
				battleStatus.Winner = issuer.Trainer
				battleStatus.BattleFinished = true
			}

		}

		toSend, err := json.Marshal(otherPlayer.selectedPokemon)

		if err != nil {
			log.Error(err)
			return
		}

		msg := &battles.BattleMessage{MsgType: battles.UPDATE_ADVERSARY_POKEMON, MsgArgs: []string{string(toSend)}}
		battles.SendMessage(msg, issuer.playerOutChannel)

		msg = &battles.BattleMessage{MsgType: battles.UPDATE_PLAYER_POKEMON, MsgArgs: []string{string(toSend)}}
		battles.SendMessage(msg, otherPlayer.playerOutChannel)

	}
}

func handlePlayerCooldownTimer(battleStatus *BattleStatus, player *trainerStatus) {

	for ; !battleStatus.BattleFinished; {
		<-player.cdTimer.C
		player.cooldown = false
	}
}
