package main

import (
	"encoding/json"
	"fmt"
	"github.com/NOVAPokemon/utils"
	ws "github.com/NOVAPokemon/utils/websockets"
	"github.com/NOVAPokemon/utils/websockets/battles"
	log "github.com/sirupsen/logrus"
	"math/rand"
	"time"
)

const DefaultCooldown = time.Second * 2

var (
	ErrInvalidMessageFormat   = "Invalid message format"
	ErrInvalidMessageType     = "Invalid message type"
	ErrPokemonSelectionPhase  = "You need to select a pokemon"
	ErrNoPokemonSelected      = "No pokemon selected"
	ErrInvalidPokemonSelected = "You do not own that pokemon"
	ErrPokemonNoHP            = "The selected pokemon has no HP"
	ErrCooldown               = "You are in cooldown"

	StatusDefended            = "Attack defended"
	StatusOpponentedDeffended = "Opponent defended attack"
)

type (
	Battle struct {
		playerIds           [2]string
		PlayersBattleStatus [2]*trainerBattleStatus

		Winner   string
		Finished bool
	}

	trainerBattleStatus struct {
		trainerPokemons []utils.Pokemon
		selectedPokemon *utils.Pokemon

		defending bool
		cooldown  bool
		cdTimer   *time.Timer

		playerInChannel  chan *string
		playerOutChannel chan *string
	}
)

func NewBattle(lobby *ws.Lobby) *Battle {

	players := [2]*trainerBattleStatus{
		{
			nil, nil,
			false, false, time.NewTimer(DefaultCooldown),
			*lobby.TrainerInChannels[0], *lobby.TrainerOutChannels[0],
		},
		{
			nil, nil,
			false, false, time.NewTimer(DefaultCooldown),
			*lobby.TrainerInChannels[1], *lobby.TrainerOutChannels[1],
		},
	}

	return &Battle{
		PlayersBattleStatus: players,
		Finished:            false,
		Winner:              "",
	}
}

func (b *Battle) addPlayer(username string, pokemons []utils.Pokemon, playerNr int) {
	b.playerIds[playerNr] = username
	b.PlayersBattleStatus[playerNr].trainerPokemons = pokemons
}

func (b *Battle) StartBattle() (string, error) {

	err := b.setupLoop()

	if err != nil {
		log.Error(err)
		panic(err)
	}

	winner, err := b.mainLoop()

	if err != nil {
		log.Error(err)
		panic(err)
	}

	return winner, err
}

func (b *Battle) setupLoop() error {

	// TODO timer to break the loop if a player takes too long to select
	players := b.PlayersBattleStatus

	// loops until both players have selected a pokemon
	for ; players[0].selectedPokemon == nil || players[1].selectedPokemon == nil; {
		select {

		case msgStr := <-players[0].playerInChannel:

			err, msg := ws.ParseMessage(msgStr)

			if err != nil {
				errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{ErrInvalidMessageType}}
				ws.SendMessage(errMsg, players[0].playerOutChannel)
				break
			}

			if msg.MsgType != battles.SELECT_POKEMON {
				errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{ErrPokemonSelectionPhase}}
				ws.SendMessage(errMsg, players[0].playerOutChannel)
				break
			}

			b.handleSelectPokemon(msg, players[0], players[1])

		case msgStr := <-players[1].playerInChannel:

			err, msg := ws.ParseMessage(msgStr)

			if err != nil {
				errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{ErrInvalidMessageType}}
				ws.SendMessage(errMsg, players[0].playerOutChannel)
				break
			}

			if msg.MsgType != battles.SELECT_POKEMON {
				errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{ErrInvalidMessageType}}
				ws.SendMessage(errMsg, players[0].playerOutChannel)
				break
			}

			b.handleSelectPokemon(msg, players[1], players[0])
		}
	}

	log.Infof("Battle setup finished")
	return nil
}

func (b *Battle) mainLoop() (string, error) {

	go b.handlePlayerCooldownTimer(b.PlayersBattleStatus[0])
	go b.handlePlayerCooldownTimer(b.PlayersBattleStatus[1])

	// main battle loop
	for ; !b.Finished; {
		select {

		case msgStr := <-b.PlayersBattleStatus[0].playerInChannel:

			err, msg := ws.ParseMessage(msgStr)
			if err != nil {
				errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{ErrInvalidMessageFormat}}
				ws.SendMessage(errMsg, b.PlayersBattleStatus[0].playerOutChannel)
			}

			b.handlePlayerMove(msg, b.PlayersBattleStatus[0], b.PlayersBattleStatus[1])

		case msgStr := <-b.PlayersBattleStatus[1].playerInChannel:

			err, msg := ws.ParseMessage(msgStr)
			if err != nil {
				errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{ErrInvalidMessageFormat}}
				ws.SendMessage(errMsg, b.PlayersBattleStatus[0].playerOutChannel)
			}

			b.handlePlayerMove(msg, b.PlayersBattleStatus[1], b.PlayersBattleStatus[0])
		}
	}

	return b.Winner, nil
}

// handles the reception of a move from a player.
func (b *Battle) handlePlayerMove(message *ws.Message, issuer *trainerBattleStatus, otherPlayer *trainerBattleStatus) {

	switch message.MsgType {

	case battles.ATTACK:
		b.handleAttackMove(issuer, otherPlayer)

	case battles.DEFEND:
		b.handleDefendMove(issuer, otherPlayer)

	case battles.USE_ITEM:
		//TODO

	case battles.SELECT_POKEMON:
		b.handleSelectPokemon(message, issuer, otherPlayer)

	default:
		msg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{fmt.Sprintf(ErrInvalidMessageType)}}
		ws.SendMessage(msg, issuer.playerOutChannel)
		return
	}
}

// handles the reception of a SELECT_POKEMON message, sends error message if message is not of type SELECT_POKEMON
func (b *Battle) handleSelectPokemon(message *ws.Message, issuer *trainerBattleStatus, otherPlayer *trainerBattleStatus) {

	if len(message.MsgArgs) < 1 {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{ErrNoPokemonSelected}}
		ws.SendMessage(errMsg, issuer.playerOutChannel)
		return
	}

	selectedPokemon := message.MsgArgs[0]

	for _, pokemon := range issuer.trainerPokemons {
		if pokemon.Id.Hex() == selectedPokemon {

			if pokemon.HP <= 0 {
				// pokemon is dead
				msg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{fmt.Sprintf(ErrPokemonNoHP)}}
				ws.SendMessage(msg, issuer.playerOutChannel)

			}

			issuer.selectedPokemon = &pokemon

			log.Infof("player 0 selected pokemon %+v", pokemon)
			toSend, err := json.Marshal(pokemon)

			if err != nil {
				log.Error(err)

			}

			log.Infof("%s", toSend)

			msg := ws.Message{MsgType: battles.UPDATE_ADVERSARY_POKEMON, MsgArgs: []string{string(toSend)}}
			ws.SendMessage(msg, otherPlayer.playerOutChannel)
			msg = ws.Message{MsgType: battles.UPDATE_PLAYER_POKEMON, MsgArgs: []string{string(toSend)}}
			ws.SendMessage(msg, issuer.playerOutChannel)
			return
		}
	}

	errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{ErrInvalidPokemonSelected}}
	ws.SendMessage(errMsg, issuer.playerOutChannel)
}

func (b *Battle) handleDefendMove(issuer *trainerBattleStatus, otherPlayer *trainerBattleStatus) {

	// if the pokemon is dead, player must select a new pokemon
	if issuer.selectedPokemon.HP == 0 {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{ErrPokemonNoHP}}
		ws.SendMessage(errMsg, issuer.playerOutChannel)
		return
	}

	// if player has moved recently and is in cooldown, discard move
	if issuer.cooldown {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{ErrCooldown}}
		ws.SendMessage(errMsg, issuer.playerOutChannel)
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

	msg := ws.Message{MsgType: battles.UPDATE_PLAYER, MsgArgs: []string{string(toSend)}}
	ws.SendMessage(msg, issuer.playerOutChannel)

	msg = ws.Message{MsgType: battles.UPDATE_ADVERSARY, MsgArgs: []string{string(toSend)}}
	ws.SendMessage(msg, otherPlayer.playerOutChannel)

	go func() { // after the player cooldown expires, remove defending status
		<-issuer.cdTimer.C
		issuer.defending = false
	}()
}

func (b *Battle) handleAttackMove(issuer *trainerBattleStatus, otherPlayer *trainerBattleStatus) {

	if issuer.selectedPokemon.HP == 0 {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{ErrPokemonNoHP}}
		ws.SendMessage(errMsg, issuer.playerOutChannel)
		return
	}

	// if player has moved recently and is in cooldown, discard move
	if issuer.cooldown {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{ErrCooldown}}
		ws.SendMessage(errMsg, issuer.playerOutChannel)
		return
	}
	issuer.cdTimer.Reset(DefaultCooldown)
	issuer.cooldown = true

	if otherPlayer.defending {

		msg := ws.Message{MsgType: battles.STATUS, MsgArgs: []string{StatusDefended}}
		ws.SendMessage(msg, issuer.playerOutChannel)

		msg = ws.Message{MsgType: battles.STATUS, MsgArgs: []string{StatusDefended}}
		ws.SendMessage(msg, issuer.playerOutChannel)
		return

	} else {

		otherPlayer.selectedPokemon.HP -= int(float32(issuer.selectedPokemon.Damage) * rand.Float32())

		if otherPlayer.selectedPokemon.HP < 0 {
			otherPlayer.selectedPokemon.HP = 0

			allPokemonsDead := true
			for _, pokemon := range otherPlayer.trainerPokemons {
				if pokemon.HP > 0 {
					allPokemonsDead = false
					break
				}
			}

			if allPokemonsDead {
				// battle is finished
				b.Finished = true
			}

		}

		toSend, err := json.Marshal(otherPlayer.selectedPokemon)

		if err != nil {
			log.Error(err)
			return
		}

		msg := ws.Message{MsgType: battles.UPDATE_ADVERSARY_POKEMON, MsgArgs: []string{string(toSend)}}
		ws.SendMessage(msg, issuer.playerOutChannel)

		msg = ws.Message{MsgType: battles.UPDATE_PLAYER_POKEMON, MsgArgs: []string{string(toSend)}}
		ws.SendMessage(msg, otherPlayer.playerOutChannel)

	}
}

func (b *Battle) handlePlayerCooldownTimer(player *trainerBattleStatus) {

	for ; !b.Finished; {
		<-player.cdTimer.C
		player.cooldown = false
	}
}
