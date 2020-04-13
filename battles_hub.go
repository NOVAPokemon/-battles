package main

import (
	"encoding/json"
	"fmt"
	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/api"
	"github.com/NOVAPokemon/utils/clients"
	"github.com/NOVAPokemon/utils/experience"
	"github.com/NOVAPokemon/utils/notifications"
	"github.com/NOVAPokemon/utils/tokens"
	ws "github.com/NOVAPokemon/utils/websockets"
	"github.com/NOVAPokemon/utils/websockets/battles"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"net/http"
	"runtime"
	"sync"
	"time"
)

type keyType = primitive.ObjectID
type valueType = *Battle

type BattleHub struct {
	notificationClient *clients.NotificationClient

	AwaitingLobbies sync.Map
	QueuedBattles   sync.Map
	ongoingBattles  sync.Map
}

var hub *BattleHub
var httpClient = &http.Client{}

var (
	ErrBattleNotExists      = errors.New("Battle does not exist")
	ErrInConnection         = errors.New("Connection Error")
	ErrPlayerNotOnline      = errors.New("Challenged player not online")
	ErrNotEnoughPokemons    = errors.New("Not enough pokemons")
	ErrTooManyPokemons      = errors.New("Not enough pokemons")
	ErrInvalidPokemonHashes = errors.New("Ivalid pokemon hashes")
)

func init() {
	hub = &BattleHub{
		notificationClient: clients.NewNotificationClient(fmt.Sprintf("%s:%d", utils.Host, utils.NotificationsPort), nil),
		AwaitingLobbies:    sync.Map{},
		QueuedBattles:      sync.Map{},
		ongoingBattles:     sync.Map{},
	}
}

func HandleGetCurrentLobbies(w http.ResponseWriter, _ *http.Request) {
	var availableLobbies []utils.Lobby
	hub.QueuedBattles.Range(func(key, value interface{}) bool {
		toAdd := utils.Lobby{
			Id:       key.(keyType),
			Username: value.(valueType).playerIds[0],
		}
		availableLobbies = append(availableLobbies, toAdd)

		return true
	})

	log.Infof("Request for available lobbies, response: %+v", availableLobbies)
	js, err := json.Marshal(availableLobbies)

	if err != nil {
		log.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(js)

	if err != nil {
		log.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

}

func HandleQueueForBattle(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error(err)
		http.Error(w, ErrInConnection.Error(), http.StatusInternalServerError)
		_ = conn.Close()
		return
	}

	authToken, err := tokens.ExtractAndVerifyAuthToken(r.Header)

	if err != nil {
		log.Error()
		_ = conn.WriteMessage(websocket.TextMessage, []byte("No auth token"))
		_ = conn.Close()
		return
	}

	trainersClient := clients.NewTrainersClient(fmt.Sprintf("%s:%d", utils.Host, utils.TrainersPort), httpClient)

	log.Infof("New player queued for battle: %s", authToken.Username)
	statsToken, pokemons, err := extractTokensForBattle(trainersClient, authToken.Username, r)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
		_ = conn.Close()
		return
	}

	log.Info("Player pokemons:")
	for _, p := range pokemons {
		log.Infof("%s\t:\t%s", p.Id.Hex(), p.Species)
	}

	hasOne := false
	hub.QueuedBattles.Range(func(key, value interface{}) bool {
		hasOne = true
		hub.QueuedBattles.Delete(key)
		battle := value.(valueType)
		battle.addPlayer(authToken.Username, pokemons, statsToken, conn, 1, r.Header.Get(tokens.AuthTokenHeaderName))
		startBattle(trainersClient, key.(keyType), battle)
		return false
	})

	if hasOne {
		return
	}

	lobbyId := primitive.NewObjectID()
	battleLobby := ws.NewLobby(lobbyId)
	battle := NewBattle(battleLobby)
	battle.addPlayer(authToken.Username, pokemons, statsToken, conn, 0, r.Header.Get(tokens.AuthTokenHeaderName))
	hub.QueuedBattles.Store(lobbyId, battle)

	go func() {
		defer func() {
			if !battleLobby.Started {
				log.Error("Player left queue")
				defer hub.QueuedBattles.Delete(lobbyId)
			}
		}()
		for !battleLobby.Started {
			select {
			case <-battleLobby.EndConnectionChannels[0]:
				return
			case <-battleLobby.EndConnectionChannels[1]:
				return
			case <-battle.StartChannel:
				return
			}

		}
	}()
}

func HandleChallengeToBattle(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error(err)
		http.Error(w, ErrInConnection.Error(), http.StatusInternalServerError)
		_ = conn.Close()
		return
	}

	authToken, err := tokens.ExtractAndVerifyAuthToken(r.Header)

	if err != nil {
		log.Error()
		_ = conn.WriteMessage(websocket.TextMessage, []byte("No auth token"))
		_ = conn.Close()
		return
	}

	trainersClient := clients.NewTrainersClient(fmt.Sprintf("%s:%d", utils.Host, utils.TrainersPort), httpClient)
	statsToken, pokemons, err := extractTokensForBattle(trainersClient, authToken.Username, r)

	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
		_ = conn.Close()
		return
	}

	log.Infof("Player pokemons:")
	for _, p := range pokemons {
		log.Infof("%s\t:\t%s", p.Id.Hex(), p.Species)
	}

	challengedPlayer := mux.Vars(r)[api.TargetPlayerIdPathvar]
	log.Infof("Player %s challenged %s for a battle", authToken.Username, challengedPlayer)

	lobbyId := primitive.NewObjectID()
	toSend := utils.Notification{
		Id:       primitive.NewObjectID(),
		Username: challengedPlayer,
		Type:     notifications.ChallengeToBattle,
		Content:  []byte(lobbyId.Hex()),
	}

	log.Infof("Sending notification: Id:%s Content:%s to %s", toSend.Id.Hex(), string(toSend.Content), toSend.Username)
	err = hub.notificationClient.AddNotification(toSend, r.Header.Get(tokens.AuthTokenHeaderName))

	if err != nil {
		http.Error(w, ErrPlayerNotOnline.Error(), http.StatusInternalServerError)
		return
	}

	battleLobby := ws.NewLobby(lobbyId)
	battle := NewBattle(battleLobby)
	battle.addPlayer(authToken.Username, pokemons, statsToken, conn, 0, r.Header.Get(tokens.AuthTokenHeaderName))
	hub.AwaitingLobbies.Store(lobbyId, battle)
	log.Infof("Created lobby: %s", battleLobby.Id.Hex())

	go func() {
		timer := time.NewTimer(10 * time.Second)
		<-timer.C
		if !battleLobby.Started {
			log.Error("Closing lobby because no player joined")
			ws.CloseLobby(battleLobby)
		}
	}()

	go func() {
		defer func() {
			if !battleLobby.Started {
				log.Error("Player left challenge lobby early")
				defer hub.AwaitingLobbies.Delete(lobbyId)
			}
		}()
		for !battleLobby.Started {
			select {
			case <-battleLobby.EndConnectionChannels[0]:
				return
			case <-battleLobby.EndConnectionChannels[1]:
				return
			case <-battle.StartChannel:
				return
			}

		}
	}()
}

func HandleAcceptChallenge(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error(err)
		http.Error(w, ErrInConnection.Error(), http.StatusInternalServerError)
		_ = conn.Close()
		return
	}

	authToken, err := tokens.ExtractAndVerifyAuthToken(r.Header)

	if err != nil {
		log.Error()
		_ = conn.WriteMessage(websocket.TextMessage, []byte("No auth token"))
		_ = conn.Close()
		return
	}

	trainersClient := clients.NewTrainersClient(fmt.Sprintf("%s:%d", utils.Host, utils.TrainersPort), httpClient)
	statsToken, pokemons, err := extractTokensForBattle(trainersClient, authToken.Username, r)

	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
		_ = conn.Close()
		return
	}

	log.Infof("Player pokemons:")
	for _, p := range pokemons {
		log.Infof("%s\t:\t%s", p.Id.Hex(), p.Species)
	}

	lobbyId, err := primitive.ObjectIDFromHex(mux.Vars(r)[api.BattleIdPathVar])
	value, ok := hub.AwaitingLobbies.Load(lobbyId)
	battle := value.(valueType)

	if !ok {
		http.Error(w, ErrBattleNotExists.Error(), http.StatusNotFound)
		_ = conn.Close()
		return
	}

	hub.AwaitingLobbies.Delete(lobbyId)
	log.Infof("Trainer %s joined ws %s", authToken.Username, mux.Vars(r)[api.BattleIdPathVar])
	battle.addPlayer(authToken.Username, pokemons, statsToken, conn, 1, r.Header.Get(tokens.AuthTokenHeaderName))
	startBattle(trainersClient, lobbyId, battle)
}

func startBattle(trainersClient *clients.TrainersClient, battleId primitive.ObjectID, battle *Battle) {
	defer log.Info("Cleaning lobby")
	defer hub.ongoingBattles.Delete(battleId)

	log.Infof("Battle %s starting...", battleId.Hex())
	hub.ongoingBattles.Store(battleId, battle)
	winner, err := battle.StartBattle()

	if err != nil {
		log.Errorf("Battle %s finished with error: ", err)
		log.Error(err)
	} else {
		log.Infof("Battle %s finished, winner is: %s", battleId, winner)
		err := commitBattleResults(trainersClient, battleId.Hex(), battle)
		if err != nil {
			log.Error(err)
		}
	}

	// finish battle
	battle.FinishBattle(battle.Winner)
	log.Warnf("Active goroutines: %d", runtime.NumGoroutine())
}

func extractTokensForBattle(trainersClient *clients.TrainersClient, username string, r *http.Request) (*utils.TrainerStats, map[string]*utils.Pokemon, error) {

	pokemonTkns, err := tokens.ExtractAndVerifyPokemonTokens(r.Header)

	if err != nil {
		log.Error(err)
		return nil, nil, err
	}

	if len(pokemonTkns) > battles.PokemonsPerBattle {
		log.Error(ErrTooManyPokemons)
		return nil, nil, ErrTooManyPokemons
	}

	if len(pokemonTkns) < battles.PokemonsPerBattle {
		log.Error(ErrNotEnoughPokemons)
		return nil, nil, ErrNotEnoughPokemons
	}

	trainerStatsToken, err := tokens.ExtractAndVerifyTrainerStatsToken(r.Header)
	if err != nil {
		log.Error(err)
		return nil, nil, err
	}

	valid, err := trainersClient.VerifyTrainerStats(username, trainerStatsToken.TrainerHash, r.Header.Get(tokens.AuthTokenHeaderName))

	if err != nil || !*valid {
		log.Error("Invalid trainer stats token: ", err)
		return nil, nil, err
	}

	pokemons := make(map[string]*utils.Pokemon, len(pokemonTkns))
	pokemonHashes := make(map[string][]byte, len(pokemonTkns))
	for _, pokemonTkn := range pokemonTkns {
		pokemonId := pokemonTkn.Pokemon.Id.Hex()
		pokemons[pokemonId] = &pokemonTkn.Pokemon
		pokemonHashes[pokemonId] = pokemonTkn.PokemonHash
	}

	upToDate, err := trainersClient.VerifyPokemons(username, pokemonHashes, r.Header.Get(tokens.AuthTokenHeaderName))
	if err != nil {
		log.Error("Invalid trainer stats token: ", err)
		return nil, nil, err
	}

	if !*upToDate {
		log.Error("pokemon tokens not up to date")
		return nil, nil, ErrInvalidPokemonHashes
	}

	return &trainerStatsToken.TrainerStats, pokemons, nil
}

func commitBattleResults(trainersClient *clients.TrainersClient, battleId string, battle *Battle) error {
	log.Infof("Commiting battle results from battle %s, with winner: %s", battleId, battle.Winner)

	player0 := battle.PlayersBattleStatus[0]
	player1 := battle.PlayersBattleStatus[1]

	for id, pokemon := range player0.trainerPokemons {
		pokemon.XP += experience.GetPokemonExperienceGainFromBattle(player0.username == battle.Winner)
		_, err := trainersClient.UpdateTrainerPokemon(player0.username, id, *pokemon)
		if err != nil {
			log.Errorf("An error occurred updating pokemons from user %s : %s", player1.username, err.Error())
		}
	}

	err := sendPokemonTokensToUser(trainersClient, battle, 0)
	if err != nil {
		log.Error(err)
		return err
	}

	for id, pokemon := range player1.trainerPokemons {
		pokemon.XP += experience.GetPokemonExperienceGainFromBattle(player1.username == battle.Winner)
		_, err := trainersClient.UpdateTrainerPokemon(player1.username, id, *pokemon)
		if err != nil {
			log.Errorf("An error occurred updating pokemons from user %s : %s", player1.username, err.Error())
		}
	}

	err = sendPokemonTokensToUser(trainersClient, battle, 1)
	if err != nil {
		log.Error(err)
		return err
	}

	log.Info("Updated pokemons from both players")

	err = addExperienceToPlayer(trainersClient, player0.username, battle.AuthTokens[0], battle.PlayersBattleStatus[0].trainerStats,
		battle.Lobby.TrainerOutChannels[0], player0.username == battle.Winner)
	if err != nil {
		log.Error(err)
		return err
	}

	err = addExperienceToPlayer(trainersClient, player1.username, battle.AuthTokens[1], battle.PlayersBattleStatus[1].trainerStats,
		battle.Lobby.TrainerOutChannels[1], player1.username == battle.Winner)
	if err != nil {
		log.Error(err)
		return err
	}

	return nil
}

func sendPokemonTokensToUser(trainersClient *clients.TrainersClient, battle *Battle, playerNr int) error {
	toSend := make([]string, len(trainersClient.PokemonTokens)+1)
	toSend[0] = tokens.PokemonsTokenHeaderName
	i := 1
	for _, v := range trainersClient.PokemonTokens {
		toSend[i] = v
		i++
	}

	setTokensMessage := &ws.Message{
		MsgType: battles.SET_TOKEN,
		MsgArgs: toSend,
	}
	ws.SendMessage(*setTokensMessage, *battle.Lobby.TrainerOutChannels[playerNr])
	return nil
}

func addExperienceToPlayer(trainersClient *clients.TrainersClient, username string, authToken string, stats *utils.TrainerStats, outChan *chan *string, winner bool) error {
	stats.XP += experience.GetPokemonTrainerGainFromBattle(winner)
	_, err := trainersClient.UpdateTrainerStats(username, *stats, authToken)

	if err != nil {
		return err
	}

	toSend := []string{tokens.StatsTokenHeaderName, trainersClient.TrainerStatsToken}
	setTokensMessage := &ws.Message{
		MsgType: battles.SET_TOKEN,
		MsgArgs: toSend,
	}
	ws.SendMessage(*setTokensMessage, *outChan)
	return nil
}
