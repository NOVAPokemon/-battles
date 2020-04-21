package main

import (
	"encoding/json"
	"fmt"
	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/api"
	"github.com/NOVAPokemon/utils/clients"
	"github.com/NOVAPokemon/utils/experience"
	"github.com/NOVAPokemon/utils/items"
	"github.com/NOVAPokemon/utils/notifications"
	"github.com/NOVAPokemon/utils/pokemons"
	"github.com/NOVAPokemon/utils/tokens"
	ws "github.com/NOVAPokemon/utils/websockets"
	"github.com/NOVAPokemon/utils/websockets/battles"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"io/ioutil"
	"net/http"
	"runtime"
	"sync"
	"time"
)

type keyType = primitive.ObjectID
type valueType = *Battle

type BattleHub struct {
	notificationClient *clients.NotificationClient
	AwaitingLobbies    sync.Map
	QueuedBattles      sync.Map
	ongoingBattles     sync.Map
}

const configFilename = "configs.json"

var hub *BattleHub
var httpClient = &http.Client{}
var config *BattleServerConfig

var (
	ErrBattleNotExists      = errors.New("Battle does not exist")
	ErrInConnection         = errors.New("Connection Error")
	ErrPlayerNotOnline      = errors.New("Challenged player not online")
	ErrNotEnoughPokemons    = errors.New("Not enough pokemons")
	ErrTooManyPokemons      = errors.New("Not enough pokemons")
	ErrInvalidPokemonHashes = errors.New("Invalid pokemon hashes")
)

func init() {
	config = loadConfig()
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
			Username: value.(valueType).PlayersBattleStatus[0].Username,
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
	trainerItems, statsToken, pokemonsForBattle, err := extractAndVerifyTokensForBattle(trainersClient, authToken.Username, r)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
		_ = conn.Close()
		return
	}

	log.Info("Player pokemons:")
	for _, p := range pokemonsForBattle {
		log.Infof("%s\t:\t%s", p.Id.Hex(), p.Species)
	}

	hasOne := false
	hub.QueuedBattles.Range(func(key, value interface{}) bool {
		hasOne = true
		hub.QueuedBattles.Delete(key)
		battle := value.(valueType)
		battle.addPlayer(authToken.Username, pokemonsForBattle, statsToken, trainerItems, conn, 1, r.Header.Get(tokens.AuthTokenHeaderName))
		startBattle(trainersClient, key.(keyType), battle)
		return false
	})

	if hasOne {
		return
	}

	lobbyId := primitive.NewObjectID()
	battleLobby := ws.NewLobby(lobbyId)
	battle := NewBattle(battleLobby, config.DefaultCooldown)
	battle.addPlayer(authToken.Username, pokemonsForBattle, statsToken, trainerItems, conn, 0, r.Header.Get(tokens.AuthTokenHeaderName))
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
	trainerItems, statsToken, pokemonsForBattle, err := extractAndVerifyTokensForBattle(trainersClient, authToken.Username, r)

	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
		_ = conn.Close()
		return
	}

	log.Infof("Player pokemons:")
	for _, p := range pokemonsForBattle {
		log.Infof("%s\t:\t%s", p.Id.Hex(), p.Species)
	}

	challengedPlayer := mux.Vars(r)[api.TargetPlayerIdPathvar]
	log.Infof("Player %s challenged %s for a battle", authToken.Username, challengedPlayer)

	lobbyId := primitive.NewObjectID()
	toSend := utils.Notification{
		Id:               primitive.NewObjectID(),
		Username:         challengedPlayer,
		Type:             notifications.ChallengeToBattle,
		Content:          []byte(lobbyId.Hex()),
		TimestampEmitted: ws.MakeTimestamp(),
	}

	log.Infof("Sending notification: Id:%s Content:%s to %s", toSend.Id.Hex(), string(toSend.Content), toSend.Username)
	err = hub.notificationClient.AddNotification(toSend, r.Header.Get(tokens.AuthTokenHeaderName))

	if err != nil {
		http.Error(w, ErrPlayerNotOnline.Error(), http.StatusInternalServerError)
		return
	}

	battleLobby := ws.NewLobby(lobbyId)
	battle := NewBattle(battleLobby, config.DefaultCooldown)
	battle.addPlayer(authToken.Username, pokemonsForBattle, statsToken, trainerItems, conn, 0, r.Header.Get(tokens.AuthTokenHeaderName))
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
	trainerItems, statsToken, pokemonsForBattle, err := extractAndVerifyTokensForBattle(trainersClient, authToken.Username, r)

	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
		_ = conn.Close()
		return
	}

	log.Infof("Player pokemons:")
	for _, p := range pokemonsForBattle {
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
	battle.addPlayer(authToken.Username, pokemonsForBattle, statsToken, trainerItems, conn, 1, r.Header.Get(tokens.AuthTokenHeaderName))
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

func extractAndVerifyTokensForBattle(trainersClient *clients.TrainersClient, username string, r *http.Request) (map[string]items.Item, *utils.TrainerStats, map[string]*pokemons.Pokemon, error) {

	pokemonTkns, err := tokens.ExtractAndVerifyPokemonTokens(r.Header)

	// pokemons

	if err != nil {
		log.Error(err)
		return nil, nil, nil, err
	}

	if len(pokemonTkns) > config.PokemonsPerBattle {
		log.Error(ErrTooManyPokemons)
		return nil, nil, nil, ErrTooManyPokemons
	}

	if len(pokemonTkns) < config.PokemonsPerBattle {
		log.Error(ErrNotEnoughPokemons)
		return nil, nil, nil, ErrNotEnoughPokemons
	}

	pokemonsInToken := make(map[string]*pokemons.Pokemon, len(pokemonTkns))
	pokemonHashes := make(map[string][]byte, len(pokemonTkns))
	for _, pokemonTkn := range pokemonTkns {
		pokemonId := pokemonTkn.Pokemon.Id.Hex()
		pokemonsInToken[pokemonId] = &pokemonTkn.Pokemon
		pokemonHashes[pokemonId] = pokemonTkn.PokemonHash
	}

	valid, err := trainersClient.VerifyPokemons(username, pokemonHashes, r.Header.Get(tokens.AuthTokenHeaderName))
	if err != nil {
		log.Error("Invalid trainer stats token: ", err)
		return nil, nil, nil, err
	}

	if !*valid {
		log.Error("pokemon tokens not up to date")
		return nil, nil, nil, ErrInvalidPokemonHashes
	}

	// stats

	trainerStatsToken, err := tokens.ExtractAndVerifyTrainerStatsToken(r.Header)
	if err != nil {
		log.Error(err)
		return nil, nil, nil, err
	}

	valid, err = trainersClient.VerifyTrainerStats(username, trainerStatsToken.TrainerHash, r.Header.Get(tokens.AuthTokenHeaderName))

	if err != nil || !*valid {
		log.Error("Invalid trainer stats token: ", err)
		return nil, nil, nil, err
	}

	// items

	itemsToken, err := tokens.ExtractAndVerifyItemsToken(r.Header)

	if err != nil {
		log.Error(err)
		return nil, nil, nil, err
	}

	valid, err = trainersClient.VerifyItems(username, itemsToken.ItemsHash, r.Header.Get(tokens.AuthTokenHeaderName))

	if err != nil || !*valid {
		log.Error("Invalid trainer items token: ", err)
		return nil, nil, nil, err
	}

	return itemsToken.Items, &trainerStatsToken.TrainerStats, pokemonsInToken, nil
}

func commitBattleResults(trainersClient *clients.TrainersClient, battleId string, battle *Battle) error {

	log.Infof("Committing battle results from battle %s, with winner: %s", battleId, battle.Winner)

	experienceGain := experience.GetPokemonExperienceGainFromBattle(battle.Winner == battle.PlayersBattleStatus[0].Username)
	err := UpdateTrainerPokemons(trainersClient, *battle.PlayersBattleStatus[0], battle.AuthTokens[0], *battle.Lobby.TrainerOutChannels[0], experienceGain)
	if err != nil {
		log.Error(err)
		return err
	}

	experienceGain = experience.GetPokemonExperienceGainFromBattle(battle.Winner == battle.PlayersBattleStatus[1].Username)
	err = UpdateTrainerPokemons(trainersClient, *battle.PlayersBattleStatus[1], battle.AuthTokens[1], *battle.Lobby.TrainerOutChannels[1], experienceGain)
	if err != nil {
		log.Error(err)
		return err
	}

	//Update trainer stats: add experience
	experienceGain = experience.GetTrainerExperienceGainFromBattle(battle.Winner == battle.PlayersBattleStatus[0].Username)
	err = AddExperienceToPlayer(trainersClient, *battle.PlayersBattleStatus[0], battle.AuthTokens[0], *battle.Lobby.TrainerOutChannels[0], experienceGain)
	if err != nil {
		log.Error(err)
		return err
	}

	experienceGain = experience.GetTrainerExperienceGainFromBattle(battle.Winner == battle.PlayersBattleStatus[1].Username)
	err = AddExperienceToPlayer(trainersClient, *battle.PlayersBattleStatus[1], battle.AuthTokens[1], *battle.Lobby.TrainerOutChannels[1], experienceGain)
	if err != nil {
		log.Error(err)
		return err
	}

	// Update trainer items, removing the items that were used during the battle
	err = RemoveUsedItems(trainersClient, *battle.PlayersBattleStatus[0], battle.AuthTokens[0], *battle.Lobby.TrainerOutChannels[0])
	if err != nil {
		log.Error(err)
		return err
	}

	err = RemoveUsedItems(trainersClient, *battle.PlayersBattleStatus[1], battle.AuthTokens[1], *battle.Lobby.TrainerOutChannels[1])
	if err != nil {
		log.Error(err)
		return err
	}

	return nil
}

func RemoveUsedItems(trainersClient *clients.TrainersClient, player battles.TrainerBattleStatus, authToken string, outChan chan *string) error {

	usedItems := player.UsedItems

	if len(usedItems) == 0 {
		return nil
	}

	itemIds := make([]string, 0, len(usedItems))

	for itemId := range usedItems {
		itemIds = append(itemIds, itemId)
	}

	_, err := trainersClient.RemoveItemsFromBag(player.Username, itemIds, authToken)

	if err != nil {
		return err
	}

	setTokensMessage := battles.SetTokenMessage{
		TokenField:   tokens.ItemsTokenHeaderName,
		TokensString: []string{trainersClient.ItemsToken},
	}.SerializeToWSMessage()
	ws.SendMessage(*setTokensMessage, outChan)
	return nil
}

func UpdateTrainerPokemons(trainersClient *clients.TrainersClient, player battles.TrainerBattleStatus, authToken string, outChan chan *string, xpAmount float64) error {

	// updates pokemon status after battle: adds XP and updates HP
	//player 0

	for id, pokemon := range player.TrainerPokemons {
		pokemon.XP += xpAmount
		pokemon.HP = pokemon.MaxHP
		_, err := trainersClient.UpdateTrainerPokemon(player.Username, id, *pokemon, authToken)
		if err != nil {
			log.Errorf("An error occurred updating pokemons from user %s : %s", player.Username, err.Error())
		}
	}

	toSend := make([]string, len(trainersClient.PokemonTokens))
	i := 0
	for _, v := range trainersClient.PokemonTokens {
		toSend[i] = v
		i++
	}

	setTokensMessage := battles.SetTokenMessage{
		TokenField:   tokens.PokemonsTokenHeaderName,
		TokensString: toSend,
	}.SerializeToWSMessage()
	ws.SendMessage(*setTokensMessage, outChan)
	return nil
}

func AddExperienceToPlayer(trainersClient *clients.TrainersClient, player battles.TrainerBattleStatus, authToken string, outChan chan *string, XPAmount float64) error {

	stats := player.TrainerStats
	stats.XP += XPAmount
	_, err := trainersClient.UpdateTrainerStats(player.Username, *stats, authToken)

	if err != nil {
		return err
	}

	setTokensMessage := battles.SetTokenMessage{
		TokenField:   tokens.StatsTokenHeaderName,
		TokensString: []string{trainersClient.TrainerStatsToken},
	}.SerializeToWSMessage()
	ws.SendMessage(*setTokensMessage, outChan)
	return nil
}

func loadConfig() *BattleServerConfig {
	fileData, err := ioutil.ReadFile(configFilename)
	if err != nil {
		log.Panic(err)
		return nil
	}

	var config BattleServerConfig
	err = json.Unmarshal(fileData, &config)
	if err != nil {
		log.Panic(err)
		return nil
	}

	log.Infof("Loaded config: %+v", config)
	return &config
}
