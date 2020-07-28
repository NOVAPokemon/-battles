package main

import (
	"github.com/mitchellh/mapstructure"
	"sync"
	"time"

	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/items"
	"github.com/NOVAPokemon/utils/pokemons"
	ws "github.com/NOVAPokemon/utils/websockets"
	"github.com/NOVAPokemon/utils/websockets/battles"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

type (
	battleLobby struct {
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

func createBattle(lobby *ws.Lobby, cooldown int, expected [2]string) *battleLobby {
	return &battleLobby{
		AuthTokens:          [2]string{},
		PlayersBattleStatus: [2]*battles.TrainerBattleStatus{},
		RejectChannel:       make(chan struct{}),
		Winner:              "",
		Lobby:               lobby,
		cooldown:            time.Duration(cooldown) + time.Millisecond,
		Expected:            expected,
	}
}

func (b *battleLobby) addPlayer(username string, pokemons map[string]*pokemons.Pokemon, stats *utils.TrainerStats,
	trainerItems map[string]items.Item, trainerConn *websocket.Conn, authToken string,
	commsManager ws.CommunicationManager) (int,
	error) {

	trainersJoined, err := ws.AddTrainer(b.Lobby, username, trainerConn, commsManager)

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

func (b *battleLobby) startBattle() (string, error) {
	ws.StartLobby(b.Lobby)
	err := b.setupLoop()
	if err != nil {
		return "", wrapStartBattleError(err, b.Lobby.Id)
	}

	winner, err := b.mainLoop()
	if err != nil {
		return "", wrapStartBattleError(err, b.Lobby.Id)
	}
	return winner, nil
}

func (b *battleLobby) setupLoop() error {
	players := b.PlayersBattleStatus
	startMsg := ws.StartMessage{}

	wsStartMsg := startMsg.ConvertToWSMessage()

	b.Lobby.TrainerOutChannels[0] <- wsStartMsg

	b.Lobby.TrainerOutChannels[1] <- wsStartMsg

	log.Info("Sent START message")
	// loops until both players have selected a pokemon
	for ; players[0].SelectedPokemon == nil || players[1].SelectedPokemon == nil; {
		b.logBattleStatus()
		select {
		case wsMsg, ok := <-b.Lobby.TrainerInChannels[0]:
			if ok {
				b.handleMoveInSelectionPhase(wsMsg, b.PlayersBattleStatus[0], b.Lobby.TrainerOutChannels[0])
			}
		case wsMsg, ok := <-b.Lobby.TrainerInChannels[1]:
			if ok {
				b.handleMoveInSelectionPhase(wsMsg, b.PlayersBattleStatus[1], b.Lobby.TrainerOutChannels[1])
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

func (b *battleLobby) mainLoop() (string, error) {
	// main battle loop
	for {
		select {
		case wsMsg, ok := <-b.Lobby.TrainerInChannels[0]:
			if ok {
				b.logBattleStatus()
				if battleFinished := b.handlePlayerMessage(wsMsg, b.PlayersBattleStatus[0], b.PlayersBattleStatus[1],
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

func (b *battleLobby) handleMoveInSelectionPhase(wsMsg *ws.WebsocketMsg, issuer *battles.TrainerBattleStatus,
	issuerChan chan *ws.WebsocketMsg) {

	if wsMsg.Content.AppMsgType != battles.SelectPokemon {
		errMsg := ws.ErrorMessage{
			Info:  battles.ErrorPokemonSelectionPhase.Error(),
			Fatal: false,
		}
		issuerChan <- errMsg.ConvertToWSMessageWithInfo(wsMsg.Content.RequestTrack)
		return
	}

	selectPokemonMsg := &battles.SelectPokemonMessage{}
	if err := mapstructure.Decode(wsMsg.Content.Data, selectPokemonMsg); err != nil {
		panic(err)
	}

	battles.HandleSelectPokemon(wsMsg.Content.RequestTrack, selectPokemonMsg, issuer, issuerChan)
}

// handles the reception of a move from a player.
func (b *battleLobby) handlePlayerMessage(wsMsg *ws.WebsocketMsg, issuer, otherPlayer *battles.TrainerBattleStatus,
	issuerChan, otherPlayerChan chan *ws.WebsocketMsg) (battleFinished bool) {
	msgData := wsMsg.Content.Data
	trackInfo := wsMsg.Content.RequestTrack

	switch wsMsg.Content.AppMsgType {
	case battles.Attack:
		changed := battles.HandleAttackMove(trackInfo, issuer, issuerChan, otherPlayer.Defending,
			otherPlayer.SelectedPokemon, b.cooldown)

		if changed {
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
					log.Infof("Pokemon %s:\t HP:%d", v.Id, v.HP)
				}

				log.Infof("Trainer 1 (%s) pokemons:", issuer.Username)
				for _, v := range b.PlayersBattleStatus[1].TrainerPokemons {
					log.Infof("Pokemon %s:\t HP:%d", v.Id, v.HP)
				}
				b.Winner = issuer.Username
				return true
			}

			battles.UpdateTrainerPokemon(trackInfo, *otherPlayer.SelectedPokemon, otherPlayerChan, true)
			battles.UpdateTrainerPokemon(trackInfo, *otherPlayer.SelectedPokemon, issuerChan, false)
		} else {
			// if not changed, other player defended

			issuerChan <- battles.StatusMessage{
				Message: battles.StatusEnemyDefended,
			}.ConvertToWSMessage(*trackInfo)

			otherPlayerChan <- battles.StatusMessage{
				Message: battles.StatusDefended,
			}.ConvertToWSMessage(*trackInfo)
		}
	case battles.Defend:
		battles.HandleDefendMove(trackInfo, issuer, issuerChan, b.cooldown)
		otherPlayerChan <- battles.StatusMessage{
			Message: "Enemy is defending",
		}.ConvertToWSMessage(*trackInfo)
	case battles.UseItem:
		useItemMsg := &battles.UseItemMessage{}
		if err := mapstructure.Decode(msgData, useItemMsg); err != nil {
			panic(err)
		}
		if changed := battles.HandleUseItem(trackInfo, useItemMsg, issuer, issuerChan,
			b.cooldown); changed {
			battles.UpdateTrainerPokemon(trackInfo, *issuer.SelectedPokemon, otherPlayerChan, false)
		}
	case battles.SelectPokemon:
		selectPokemonMsg := &battles.SelectPokemonMessage{}
		if err := mapstructure.Decode(msgData, selectPokemonMsg); err != nil {
			panic(err)
		}
		changed := battles.HandleSelectPokemon(trackInfo, selectPokemonMsg, issuer,
			issuerChan)
		if changed {
			battles.UpdateTrainerPokemon(trackInfo, *issuer.SelectedPokemon, otherPlayerChan, false)
		}
	default:
		log.Error(ws.NewInvalidMsgTypeError(wsMsg.Content.AppMsgType))
		issuerChan <- ws.ErrorMessage{
			Info:  ws.ErrorInvalidMessageType.Error(),
			Fatal: false,
		}.ConvertToWSMessageWithInfo(trackInfo)
	}
	return false
}

func (b *battleLobby) finishBattle() {
	toSend := ws.FinishMessage{}.ConvertToWSMessage()

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

func (b *battleLobby) logBattleStatus() {
	log.Info("----------------------------------------")
	pokemon := b.PlayersBattleStatus[0].SelectedPokemon
	log.Infof("Player 0 status: Defending:%t ; Cooldown:%t ", b.PlayersBattleStatus[0].Defending, b.PlayersBattleStatus[0].Cooldown)
	if pokemon != nil {
		log.Infof("Player 0 pokemon:ID:%s, Damage:%d, HP:%d, maxHP:%d, Species:%s", pokemon.Id, pokemon.Damage, pokemon.HP, pokemon.MaxHP, pokemon.Species)
	}

	pokemon = b.PlayersBattleStatus[1].SelectedPokemon
	log.Infof("Player 1 status: Defending:%t ; Cooldown:%t ", b.PlayersBattleStatus[1].Defending, b.PlayersBattleStatus[1].Cooldown)
	if pokemon != nil {
		log.Infof("Player 1 pokemon:ID:%s, Damage:%d, HP:%d, maxHP:%d, Species:%s", pokemon.Id, pokemon.Damage, pokemon.HP, pokemon.MaxHP, pokemon.Species)
	}
}
