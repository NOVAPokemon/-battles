package main

import (
	"encoding/json"
	"fmt"
	"github.com/NOVAPokemon/utils"
	ws "github.com/NOVAPokemon/utils/websockets"
	"github.com/NOVAPokemon/utils/websockets/battles"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
	"math/rand"
	"time"
)

const DefaultCooldown = time.Second * 2

type (
	Battle struct {
		Lobby *ws.Lobby

		playerIds           [2]string
		PlayersBattleStatus [2]*trainerBattleStatus

		Winner    string
		Selecting bool
		Finished  bool
	}

	trainerBattleStatus struct {
		username        string
		trainerPokemons map[string]*utils.Pokemon
		selectedPokemon *utils.Pokemon

		defending bool
		cooldown  bool
		cdTimer   *time.Timer
	}
)

func NewBattle(lobby *ws.Lobby) *Battle {

	return &Battle{
		PlayersBattleStatus: [2]*trainerBattleStatus{},
		Finished:            false,
		Winner:              "",
		Lobby:               lobby,
	}
}

func (b *Battle) addPlayer(username string, pokemons map[string]*utils.Pokemon, trainerConn *websocket.Conn, playerNr int) {

	player := &trainerBattleStatus{
		username,
		pokemons, nil,
		false, false, time.NewTimer(DefaultCooldown),
	}

	ws.AddTrainer(b.Lobby, username, trainerConn)
	b.PlayersBattleStatus[playerNr] = player
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
		b.logBattleStatus()
		select {

		case msgStr := <-*b.Lobby.TrainerInChannels[0]:

			msg, err := ws.ParseMessage(msgStr)

			if err != nil {
				errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrPokemonSelectionPhase}}
				ws.SendMessage(errMsg, *b.Lobby.TrainerOutChannels[0])
				continue
			}

			if msg.MsgType != battles.SELECT_POKEMON {
				errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrPokemonSelectionPhase}}
				ws.SendMessage(errMsg, *b.Lobby.TrainerOutChannels[0])
				continue
			}

			b.handleSelectPokemon(msg, players[0], *b.Lobby.TrainerOutChannels[0], *b.Lobby.TrainerOutChannels[1])

		case msgStr := <-*b.Lobby.TrainerInChannels[1]:

			msg, err := ws.ParseMessage(msgStr)

			if err != nil {
				errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrPokemonSelectionPhase}}
				ws.SendMessage(errMsg, *b.Lobby.TrainerOutChannels[1])
				continue
			}

			if msg.MsgType != battles.SELECT_POKEMON {
				errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrPokemonSelectionPhase}}
				ws.SendMessage(errMsg, *b.Lobby.TrainerOutChannels[1])
				continue
			}

			b.handleSelectPokemon(msg, players[1], *b.Lobby.TrainerOutChannels[1], *b.Lobby.TrainerOutChannels[0])
		}
	}

	log.Info("Battle setup finished")
	b.Selecting = false
	return nil
}

func (b *Battle) mainLoop() (string, error) {

	go b.handlePlayerCooldownTimer(b.PlayersBattleStatus[0])
	go b.handlePlayerCooldownTimer(b.PlayersBattleStatus[1])

	startMsg := ws.Message{MsgType: battles.START, MsgArgs: []string{}}
	ws.SendMessage(startMsg, *b.Lobby.TrainerOutChannels[0])
	ws.SendMessage(startMsg, *b.Lobby.TrainerOutChannels[1])
	log.Info("Sent START message")

	// main battle loop
	for ; !b.Finished; {
		b.logBattleStatus()
		select {

		case msgStr := <-*b.Lobby.TrainerInChannels[0]:

			msg, err := ws.ParseMessage(msgStr)
			if err != nil {
				errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrInvalidMessageFormat}}
				ws.SendMessage(errMsg, *b.Lobby.TrainerOutChannels[0])
			} else {
				b.handlePlayerMove(msg, b.PlayersBattleStatus[0], *b.Lobby.TrainerOutChannels[0], b.PlayersBattleStatus[1], *b.Lobby.TrainerOutChannels[1])
			}

		case msgStr := <-*b.Lobby.TrainerInChannels[1]:
			msg, err := ws.ParseMessage(msgStr)
			if err != nil {
				errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrInvalidMessageFormat}}
				ws.SendMessage(errMsg, *b.Lobby.TrainerOutChannels[1])

			} else {
				b.handlePlayerMove(msg, b.PlayersBattleStatus[1], *b.Lobby.TrainerOutChannels[1], b.PlayersBattleStatus[0], *b.Lobby.TrainerOutChannels[0])
			}
		}
	}

	finishMsg := ws.Message{MsgType: battles.FINISH, MsgArgs: []string{}}
	ws.SendMessage(finishMsg, *b.Lobby.TrainerOutChannels[0])
	ws.SendMessage(finishMsg, *b.Lobby.TrainerOutChannels[1])

	return b.Winner, nil
}

// handles the reception of a move from a player.
func (b *Battle) handlePlayerMove(message *ws.Message, issuer *trainerBattleStatus, issuerChan chan *string, otherPlayer *trainerBattleStatus, otherPlayerChan chan *string) {

	switch message.MsgType {

	case battles.ATTACK:
		b.handleAttackMove(issuer, issuerChan, otherPlayer, otherPlayerChan)
		break

	case battles.DEFEND:
		b.handleDefendMove(issuer, issuerChan, otherPlayerChan)
		break

	case battles.USE_ITEM:
		break
		//TODO

	case battles.SELECT_POKEMON:
		b.handleSelectPokemon(message, issuer, issuerChan, otherPlayerChan)
		break

	default:
		log.Errorf("cannot handle message type: %s ", message.MsgType)
		msg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{fmt.Sprintf(battles.ErrInvalidMessageType)}}
		ws.SendMessage(msg, issuerChan)
		return
	}
}

// handles the reception of a SELECT_POKEMON message, sends error message if message is not of type SELECT_POKEMON
func (b *Battle) handleSelectPokemon(message *ws.Message, issuer *trainerBattleStatus, issuerChan chan *string, otherPlayer chan *string) {

	if len(message.MsgArgs) < 1 {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrNoPokemonSelected}}
		ws.SendMessage(errMsg, issuerChan)
		return
	}

	selectedPokemon := message.MsgArgs[0]
	pokemon, ok := issuer.trainerPokemons[selectedPokemon]

	if !ok {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrInvalidPokemonSelected}}
		ws.SendMessage(errMsg, issuerChan)
		return
	}
	if pokemon.HP <= 0 {
		// pokemon is dead
		msg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{fmt.Sprintf(battles.ErrPokemonNoHP)}}
		ws.SendMessage(msg, issuerChan)

	}

	issuer.selectedPokemon = pokemon

	toSend, err := json.Marshal(pokemon)

	if err != nil {
		log.Error(err)

	}

	msg := ws.Message{MsgType: battles.UPDATE_ADVERSARY_POKEMON, MsgArgs: []string{string(toSend)}}
	ws.SendMessage(msg, otherPlayer)
	msg = ws.Message{MsgType: battles.UPDATE_PLAYER_POKEMON, MsgArgs: []string{string(toSend)}}
	ws.SendMessage(msg, issuerChan)
	return

}

func (b *Battle) handleDefendMove(issuer *trainerBattleStatus, issuerChan chan *string, otherPlayer chan *string) {

	// if the pokemon is dead, player must select a new pokemon
	if issuer.selectedPokemon.HP == 0 {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrPokemonNoHP}}
		ws.SendMessage(errMsg, issuerChan)
		return
	}

	// if player has moved recently and is in cooldown, discard move
	if issuer.cooldown {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrCooldown}}
		ws.SendMessage(errMsg, issuerChan)
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
	ws.SendMessage(msg, issuerChan)

	msg = ws.Message{MsgType: battles.UPDATE_ADVERSARY, MsgArgs: []string{string(toSend)}}
	ws.SendMessage(msg, otherPlayer)

	go func() { // after the player cooldown expires, remove defending status
		<-issuer.cdTimer.C
		issuer.defending = false
	}()
}

func (b *Battle) handleAttackMove(issuer *trainerBattleStatus, issuerChan chan *string, otherPlayer *trainerBattleStatus, otherPlayerChan chan *string) {

	if issuer.selectedPokemon.HP == 0 {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrPokemonNoHP}}
		ws.SendMessage(errMsg, issuerChan)
		return
	}

	// if player has moved recently and is in cooldown, discard move
	if issuer.cooldown {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrCooldown}}
		ws.SendMessage(errMsg, issuerChan)
		return
	}
	issuer.cdTimer.Reset(DefaultCooldown)
	issuer.cooldown = true

	if otherPlayer.defending {

		msg := ws.Message{MsgType: battles.STATUS, MsgArgs: []string{battles.StatusOpponentedDeffended}}
		ws.SendMessage(msg, issuerChan)

		msg = ws.Message{MsgType: battles.STATUS, MsgArgs: []string{battles.StatusDefended}}
		ws.SendMessage(msg, otherPlayerChan)
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

				log.Info("--------------BATTLE FINISHED---------------")
				log.Infof("Winner : %s", issuer.username)
				log.Infof("Trainer 0 (%s) pokemons:", issuer.username)
				for _, v := range b.PlayersBattleStatus[0].trainerPokemons {
					log.Infof("Pokemon %:\t HP:%d", v.Id.Hex(), v.HP)
				}

				log.Infof("Trainer 1 (%s) pokemons:", issuer.username)
				for _, v := range b.PlayersBattleStatus[1].trainerPokemons {
					log.Infof("Pokemon %:\t HP:%d", v.Id.Hex(), v.HP)
				}

				b.Finished = true
				b.Winner = issuer.username
			}

		}

		toSend, err := json.Marshal(otherPlayer.selectedPokemon)

		if err != nil {
			log.Error(err)
			return
		}

		msg := ws.Message{MsgType: battles.UPDATE_ADVERSARY_POKEMON, MsgArgs: []string{string(toSend)}}
		ws.SendMessage(msg, issuerChan)

		msg = ws.Message{MsgType: battles.UPDATE_PLAYER_POKEMON, MsgArgs: []string{string(toSend)}}
		ws.SendMessage(msg, otherPlayerChan)

	}
}

func (b *Battle) handlePlayerCooldownTimer(player *trainerBattleStatus) {

	for ; !b.Finished; {
		<-player.cdTimer.C
		player.cooldown = false
	}
}

func (b *Battle) logBattleStatus() {

	log.Info("----------------------------------------")
	pokemon := b.PlayersBattleStatus[0].selectedPokemon
	log.Infof("Battle %s Info: selecting:%t", b.Lobby.Id.Hex(), b.Selecting)
	log.Infof("Player 0 status: defending:%t ; cooldown:%t ", b.PlayersBattleStatus[0].defending, b.PlayersBattleStatus[0].cooldown)
	if pokemon != nil {
		log.Infof("Player 0 pokemon: id: %s; Species : %s ; Damage: %d; HP: %d ;", pokemon.Id.Hex(), pokemon.Species, pokemon.Damage, pokemon.HP)
	}

	pokemon = b.PlayersBattleStatus[1].selectedPokemon
	log.Infof("Player 1 status: defending:%t ; cooldown:%t ", b.PlayersBattleStatus[1].defending, b.PlayersBattleStatus[1].cooldown)
	if pokemon != nil {
		log.Infof("Player 1 pokemon: id: %s; Species : %s ; Damage: %d; HP: %d ;", pokemon.Id.Hex(), pokemon.Species, pokemon.Damage, pokemon.HP)
	}
}
