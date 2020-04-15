package main

import (
	"encoding/json"
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

const DefaultCooldown = time.Millisecond * 500

type (
	Battle struct {
		Lobby               *ws.Lobby
		AuthTokens          [2]string
		PlayersBattleStatus [2]*trainerBattleStatus
		Winner              string
		StartChannel        chan struct{}
		Selecting           bool
		Finished            bool
	}

	trainerBattleStatus struct {
		username        string
		trainerStats    *utils.TrainerStats
		trainerPokemons map[string]*pokemons.Pokemon
		trainerItems    map[string]items.Item
		selectedPokemon *pokemons.Pokemon

		defending bool
		cooldown  bool
		cdTimer   *time.Timer

		UsedItems map[string]items.Item
	}
)

func NewBattle(lobby *ws.Lobby) *Battle {

	return &Battle{
		AuthTokens:          [2]string{},
		PlayersBattleStatus: [2]*trainerBattleStatus{},
		Finished:            false,
		StartChannel:        make(chan struct{}),
		Winner:              "",
		Lobby:               lobby,
	}
}

func (b *Battle) addPlayer(username string, pokemons map[string]*pokemons.Pokemon, stats *utils.TrainerStats, trainerItems map[string]items.Item, trainerConn *websocket.Conn, playerNr int, authToken string) {

	player := &trainerBattleStatus{
		username,
		stats,
		pokemons,
		trainerItems,
		nil,
		false, false, time.NewTimer(DefaultCooldown),
		make(map[string]items.Item),
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
	startMsg := ws.Message{MsgType: battles.START, MsgArgs: []string{}}
	ws.SendMessage(startMsg, *b.Lobby.TrainerOutChannels[0])
	ws.SendMessage(startMsg, *b.Lobby.TrainerOutChannels[1])
	log.Info("Sent START message")

	// loops until both players have selected a pokemon
	for ; players[0].selectedPokemon == nil || players[1].selectedPokemon == nil; {
		b.logBattleStatus()
		select {

		case msgStr := <-*b.Lobby.TrainerInChannels[0]:
			b.handleSelectPokemon(msgStr, players[0], *b.Lobby.TrainerOutChannels[0], *b.Lobby.TrainerOutChannels[1])
		case msgStr := <-*b.Lobby.TrainerInChannels[1]:
			b.handleSelectPokemon(msgStr, players[1], *b.Lobby.TrainerOutChannels[1], *b.Lobby.TrainerOutChannels[0])
		case <-b.Lobby.EndConnectionChannels[0]:
			err := fmt.Sprintf("An error occurred with user %s", b.PlayersBattleStatus[1].username)
			log.Errorf("An error occurred with user %s", b.PlayersBattleStatus[0].username)
			ws.CloseLobby(b.Lobby)
			return errors.New(err)
		case <-b.Lobby.EndConnectionChannels[1]:
			err := fmt.Sprintf("An error occurred with user %s", b.PlayersBattleStatus[1].username)
			log.Errorf("An error occurred with user %s", b.PlayersBattleStatus[0].username)
			ws.CloseLobby(b.Lobby)
			return errors.New(err)
		}

	}

	log.Info("Battle setup finished")
	b.Selecting = false
	return nil
}

func (b *Battle) mainLoop() (string, error) {
	go b.handlePlayerCooldownTimer(b.PlayersBattleStatus[0])
	go b.handlePlayerCooldownTimer(b.PlayersBattleStatus[1])
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
		case <-b.Lobby.EndConnectionChannels[0]:
			err := fmt.Sprintf("An error occurred with user %s", b.PlayersBattleStatus[1].username)
			log.Errorf("An error occurred with user %s", b.PlayersBattleStatus[0].username)
			ws.CloseLobby(b.Lobby)
			return "", errors.New(err)
		case <-b.Lobby.EndConnectionChannels[1]:
			err := fmt.Sprintf("An error occurred with user %s", b.PlayersBattleStatus[1].username)
			log.Error(err)
			ws.CloseLobby(b.Lobby)
			return "", errors.New(err)
		}
	}
	return b.Winner, nil
}

// handles the reception of a move from a player.
func (b *Battle) handlePlayerMove(msgStr *string, issuer *trainerBattleStatus, issuerChan chan *string, otherPlayer *trainerBattleStatus, otherPlayerChan chan *string) {

	message, err := ws.ParseMessage(msgStr)
	if err != nil {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrInvalidMessageFormat}}
		ws.SendMessage(errMsg, *b.Lobby.TrainerOutChannels[0])
		return
	}
	switch message.MsgType {

	case battles.ATTACK:
		b.handleAttackMove(issuer, issuerChan, otherPlayer, otherPlayerChan)
		break

	case battles.DEFEND:
		b.handleDefendMove(issuer, issuerChan, otherPlayerChan)
		break

	case battles.USE_ITEM:
		b.handleUseItem(message, issuer, issuerChan, otherPlayerChan)
		break

	case battles.SELECT_POKEMON:
		b.handleSelectPokemon(msgStr, issuer, issuerChan, otherPlayerChan)
		break

	default:
		log.Errorf("cannot handle message type: %s ", message.MsgType)
		msg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{fmt.Sprintf(battles.ErrInvalidMessageType)}}
		ws.SendMessage(msg, issuerChan)
		return
	}
}

func (b *Battle) handleUseItem(message *ws.Message, issuer *trainerBattleStatus, issuerChan chan *string, otherPlayer chan *string) {

	if len(message.MsgArgs) < 1 {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrNoItemSelected}}
		ws.SendMessage(errMsg, issuerChan)
		return
	}

	if issuer.cooldown {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrCooldown}}
		ws.SendMessage(errMsg, issuerChan)
		return
	}

	selectedItem := message.MsgArgs[0]
	item, ok := issuer.trainerItems[selectedItem]

	if !ok {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrInvalidItemSelected}}
		ws.SendMessage(errMsg, issuerChan)
		return
	}

	if !item.Effect.Appliable {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrItemNotAppliable}}
		ws.SendMessage(errMsg, issuerChan)
		return
	}

	err := item.Apply(issuer.selectedPokemon)

	if err != nil {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{err.Error()}}
		ws.SendMessage(errMsg, issuerChan)
		return
	}

	updateTrainerPokemon(*issuer.selectedPokemon, issuerChan, otherPlayer)

	issuer.cdTimer.Reset(DefaultCooldown)
	issuer.cooldown = true
	issuer.UsedItems[item.Id.Hex()] = item
	delete(issuer.trainerItems, item.Id.Hex())

	msg := ws.Message{MsgType: battles.REMOVE_ITEM, MsgArgs: []string{string(item.Id.Hex())}}
	ws.SendMessage(msg, issuerChan)
	return
}

func (b *Battle) handleSelectPokemon(msgStr *string, issuer *trainerBattleStatus, issuerChan chan *string, otherPlayer chan *string) {

	message, err := ws.ParseMessage(msgStr)

	if err != nil {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrPokemonSelectionPhase}}
		ws.SendMessage(errMsg, *b.Lobby.TrainerOutChannels[0])
		return
	}

	if message.MsgType != battles.SELECT_POKEMON {
		errMsg := ws.Message{MsgType: battles.ERROR, MsgArgs: []string{battles.ErrPokemonSelectionPhase}}
		ws.SendMessage(errMsg, *b.Lobby.TrainerOutChannels[0])
		return
	}

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
	updateTrainerPokemon(*issuer.selectedPokemon, issuerChan, otherPlayer)
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
		otherPlayer.selectedPokemon.HP -= issuer.selectedPokemon.Damage

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

				log.Info("--------------BATTLE ENDED---------------")
				log.Infof("Winner : %s", issuer.username)
				log.Infof("Trainer 0 (%s) pokemons:", issuer.username)
				for _, v := range b.PlayersBattleStatus[0].trainerPokemons {
					log.Infof("Pokemon %s:\t HP:%d", v.Id.Hex(), v.HP)
				}

				log.Infof("Trainer 1 (%s) pokemons:", issuer.username)
				for _, v := range b.PlayersBattleStatus[1].trainerPokemons {
					log.Infof("Pokemon %s:\t HP:%d", v.Id.Hex(), v.HP)
				}

				b.Finished = true
				b.Winner = issuer.username
			}

		}
		updateTrainerPokemon(*otherPlayer.selectedPokemon, otherPlayerChan, issuerChan)
	}
}

func updateTrainerPokemon(pokemon pokemons.Pokemon, ownerChan chan *string, otherPlayerChan chan *string) {

	toSend, err := json.Marshal(pokemon)

	if err != nil {
		log.Error(err)
		return
	}

	msg := ws.Message{MsgType: battles.UPDATE_ADVERSARY_POKEMON, MsgArgs: []string{string(toSend)}}
	ws.SendMessage(msg, otherPlayerChan)
	msg = ws.Message{MsgType: battles.UPDATE_PLAYER_POKEMON, MsgArgs: []string{string(toSend)}}
	ws.SendMessage(msg, ownerChan)
}

func (b *Battle) FinishBattle(winner string) {
	b.Lobby.Finished = true
	finishMsg := ws.Message{MsgType: battles.FINISH, MsgArgs: []string{winner}}
	ws.SendMessage(finishMsg, *b.Lobby.TrainerOutChannels[0])
	ws.SendMessage(finishMsg, *b.Lobby.TrainerOutChannels[1])

	<-b.Lobby.EndConnectionChannels[0]
	<-b.Lobby.EndConnectionChannels[1]
	ws.CloseLobby(b.Lobby)
}

func (b *Battle) handlePlayerCooldownTimer(player *trainerBattleStatus) {

	for ; !b.Finished; {
		<-player.cdTimer.C
		player.cooldown = false
		player.defending = false
		log.Warn("Removed cooldown status")
	}
}

func (b *Battle) logBattleStatus() {

	log.Info("----------------------------------------")
	pokemon := b.PlayersBattleStatus[0].selectedPokemon
	log.Infof("Battle %s Info: selecting:%t", b.Lobby.Id.Hex(), b.Selecting)
	log.Infof("Player 0 status: defending:%t ; cooldown:%t ", b.PlayersBattleStatus[0].defending, b.PlayersBattleStatus[0].cooldown)
	if pokemon != nil {
		log.Infof("Player 0 pokemon: id: %s; Species : %s ; Damage: %d; HP: %d/%d;", pokemon.Id.Hex(), pokemon.Species, pokemon.Damage, pokemon.HP, pokemon.MaxHP)
	}

	pokemon = b.PlayersBattleStatus[1].selectedPokemon
	log.Infof("Player 1 status: defending:%t ; cooldown:%t ", b.PlayersBattleStatus[1].defending, b.PlayersBattleStatus[1].cooldown)
	if pokemon != nil {
		log.Infof("Player 1 pokemon: id: %s; Species : %s ; Damage: %d; HP: %d/%d;", pokemon.Id.Hex(), pokemon.Species, pokemon.Damage, pokemon.HP, pokemon.MaxHP)
	}
}
