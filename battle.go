package main

import (
	"errors"
	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/websockets"
	log "github.com/sirupsen/logrus"
	"strings"
	"time"
)

type BattleStatus struct {
	players        [2]*playerStatus
	BattleStarted  bool
	BattleFinished bool
}

type playerStatus struct {
	playerPokemon *utils.Pokemon
	playerCdTimer *time.Timer

	playerAttacking bool
	playerDefending bool
	playerCooldown  bool
}

type battleMessage struct {
	msgId   string
	msgType string
	msgArgs []string
}

// Message Types
const (
	ATTACK   = "ATTACK"
	USE_ITEM = "USE_ITEM"
	DEFEND   = "DEFEND"

	// Update Types
	UPDATE_POKEMON_0 = "UPDATE_POKEMON_1"
	UPDATE_POKEMON_1 = "UPDATE_POKEMON_2"

	// Setup Message Types
	SELECT_POKEMON = "SELECT_POKEMON"

	// Error
	ERROR = "ERROR"
)

const DefaultCooldown = time.Second

func StartBattle(lobby *websockets.Lobby) {
	err, battleStatus := battleSetupLoop(lobby)

	if err != nil {
		log.Error(err)
	}

	err = battleMainLoop(lobby, battleStatus)

}

func battleSetupLoop(lobby *websockets.Lobby) (error, *BattleStatus) {

	// TODO timer to break the loop if a player takes too long to select

	players := [2]*playerStatus{
		{nil, time.NewTimer(DefaultCooldown), false, false, false},
		{nil, time.NewTimer(DefaultCooldown), false, false, false},
	}

	for ; players[0].playerPokemon == nil || players[1].playerPokemon == nil; {
		select {

		case msgStr := <-*lobby.TrainerInChannels[0]:
			err, pokemon := handleSelectPokemonMessage(lobby, msgStr, *lobby.TrainerInChannels[0])

			if err != nil {
				log.Error(err)
				errMsg := &battleMessage{generateMessageId(), ERROR, []string{err.Error()}}
				sendMessage(errMsg, *lobby.TrainerOutChannels[0])
			} else {
				players[0].playerPokemon = pokemon
				log.Infof("player 0 selected pokemon %+v", pokemon)
			}


		case msgStr := <-*lobby.TrainerInChannels[1]:
			err, pokemon := handleSelectPokemonMessage(lobby, msgStr, *lobby.TrainerInChannels[1])

			if err != nil {
				log.Error(err)
				errMsg := &battleMessage{generateMessageId(), ERROR, []string{err.Error()}}
				sendMessage(errMsg, *lobby.TrainerOutChannels[1])
			} else {
				players[1].playerPokemon = pokemon
				log.Infof("player 1 selected pokemon %+v", pokemon)
			}

		}
	}

	log.Infof("Battle setup finished")
	return nil, &BattleStatus{players, false, false}
}

func handleSelectPokemonMessage(lobby *websockets.Lobby, msgStr *string, playerOutChannel chan *string) (error, *utils.Pokemon) {

	err, msg := parseMessage(msgStr)

	if err != nil {
		return err, nil
	}

	if msg.msgType != SELECT_POKEMON {
		return errors.New("need to select pokemon"), nil
	}

	if len(msg.msgArgs[0]) < 1 {
		return errors.New("invalid message"), nil
	}

	selectedPokemon := msg.msgArgs[0]

	for _, pokemon := range lobby.Trainers[0].Pokemons {
		if pokemon.Id.Hex() == selectedPokemon {
			return nil, pokemon
		}
	}

	return errors.New("you do not have the selected pokemon"), nil

}

func battleMainLoop(lobby *websockets.Lobby, battleStatus *BattleStatus) error {

	trainer0ChanIn := *lobby.TrainerInChannels[0]
	trainer0ChanOut := *lobby.TrainerOutChannels[0]
	trainer1ChanIn := *lobby.TrainerInChannels[1]
	trainer1ChanOut := *lobby.TrainerOutChannels[1]

	go handlePlayerCooldownTimers(battleStatus)

	// main battle loop
	for {
		select {
		case msgStr := <-trainer0ChanIn:
			log.Infof(*msgStr)
			_, msg := parseMessage(msgStr)
			sendMessage(msg, trainer0ChanOut)

		case msgStr := <-trainer1ChanIn:
			log.Infof(*msgStr)
			_, msg := parseMessage(msgStr)
			sendMessage(msg, trainer1ChanOut)
		}
	}
}

func handlePlayerMove(message *battleMessage, status playerStatus) (error, string) {

	// if player has moved recently and is in cooldown, discard move
	if status.playerCooldown {
		return errors.New("cooldown"), ""
	}

	status.playerCdTimer.Reset(DefaultCooldown)
	status.playerCooldown = true

	switch message.msgType {

	case ATTACK:

	case DEFEND:

	case USE_ITEM:

	case SELECT_POKEMON:

	default:
		return nil, ""
	}

	return nil, ""
}

func handlePlayerCooldownTimers(battleStatus *BattleStatus) {

	for ; !battleStatus.BattleFinished; {
		select {

		case <-battleStatus.players[0].playerCdTimer.C:
			battleStatus.players[0].playerCooldown = false

		case <-battleStatus.players[1].playerCdTimer.C:
			battleStatus.players[1].playerCooldown = false
		}
	}

}

func parseMessage(msg *string) (error, *battleMessage) {

	msgParts := strings.Split(*msg, " ")

	if len(msgParts) < 2 {
		return errors.New("invalid msg format"), nil
	}

	return nil, &battleMessage{
		msgId:   msgParts[0],
		msgType: msgParts[1],
		msgArgs: msgParts[2:],
	}
}

func sendMessage(msg *battleMessage, channel chan *string) {

	builder := strings.Builder{}

	builder.WriteString(msg.msgId)
	builder.WriteString(" ")
	builder.WriteString(msg.msgType)
	builder.WriteString(" ")

	for _, arg := range msg.msgArgs {
		builder.WriteString(arg)
		builder.WriteString(" ")
	}

	toSend := builder.String()
	channel <- &toSend

}

func generateMessageId() string {
	return ""
}
