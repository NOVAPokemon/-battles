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
	notificationsMessages "github.com/NOVAPokemon/utils/websockets/notifications"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"io/ioutil"
	"net/http"
	"os"
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

var (
	hub                 *BattleHub
	httpClient          = &http.Client{}
	config              *BattleServerConfig
	serverName          string
	serviceNameHeadless string
)

func init() {
	if aux, exists := os.LookupEnv(utils.HostnameEnvVar); exists {
		serverName = aux
	} else {
		log.Fatal("Could not load server name")
	}

	if aux, exists := os.LookupEnv(utils.HeadlessServiceNameEnvVar); exists {
		serviceNameHeadless = aux
	} else {
		log.Fatal("Could not load headless service name")
	}

	config = loadConfig()
	hub = &BattleHub{
		notificationClient: clients.NewNotificationClient(nil),
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
		utils.LogAndSendHTTPError(&w, wrapGetLobbiesError(err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	_, err = w.Write(js)
	if err != nil {
		utils.LogAndSendHTTPError(&w, wrapGetLobbiesError(err), http.StatusInternalServerError)
		return
	}
}

func HandleQueueForBattle(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		err := wrapQueueBattleError(ws.WrapUpgradeConnectionError(err))
		utils.LogAndSendHTTPError(&w, err, http.StatusInternalServerError)
		return
	}

	authToken, err := tokens.ExtractAndVerifyAuthToken(r.Header)
	if err != nil {
		// TODO Should refactor default messages and not build every time
		log.Error()
		_ = conn.WriteMessage(websocket.TextMessage, []byte("No auth token"))
		_ = conn.Close()
		return
	}

	trainersClient := clients.NewTrainersClient(httpClient)

	log.Infof("New player queued for battle: %s", authToken.Username)
	trainerItems, statsToken, pokemonsForBattle, err := extractAndVerifyTokensForBattle(trainersClient, authToken.Username, r)
	if err != nil {
		log.Error(err)
		// TODO use proper messages with constructors
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
	battle := NewBattle(battleLobby, config.DefaultCooldown, [2]string{authToken.Username, ""})
	battle.addPlayer(authToken.Username, pokemonsForBattle, statsToken, trainerItems, conn, 0, r.Header.Get(tokens.AuthTokenHeaderName))
	hub.QueuedBattles.Store(lobbyId, battle)

	go func() {
		defer func() {
			if !battleLobby.Started {
				log.Warn("Player left queue")
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
		err := wrapChallengeToBattleError(ws.WrapUpgradeConnectionError(err))
		utils.LogAndSendHTTPError(&w, err, http.StatusInternalServerError)
		return
	}

	authToken, err := tokens.ExtractAndVerifyAuthToken(r.Header)
	if err != nil {
		// TODO Should refactor default messages and not build every time
		_ = conn.WriteMessage(websocket.TextMessage, []byte("No auth token"))
		_ = conn.Close()
		return
	}

	trainersClient := clients.NewTrainersClient(httpClient)
	trainerItems, statsToken, pokemonsForBattle, err := extractAndVerifyTokensForBattle(trainersClient, authToken.Username, r)
	if err != nil {
		log.Error(wrapChallengeToBattleError(err))
		// TODO use proper messages with constructors
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
	toMarshal := notifications.WantsToBattleContent{Username: challengedPlayer,
		LobbyId:        lobbyId.Hex(),
		ServerHostname: fmt.Sprintf("%s.%s", serverName, serviceNameHeadless),
	}

	contentBytes, err := json.Marshal(toMarshal)
	if err != nil {
		log.Fatal(wrapChallengeToBattleError(err))
	}
	notification := utils.Notification{
		Id:       primitive.NewObjectID(),
		Username: challengedPlayer,
		Type:     notifications.ChallengeToBattle,
		Content:  contentBytes,
	}

	notificationMsg := notificationsMessages.NewNotificationMessage(notification)
	notificationMsg.Emit(ws.MakeTimestamp())
	notificationMsg.LogEmit(notificationsMessages.Notification)

	log.Infof("Sending notification: Id:%s Content:%s to %s", notification.Id.Hex(),
		string(notification.Content), notification.Username)
	err = hub.notificationClient.AddNotification(&notificationMsg, r.Header.Get(tokens.AuthTokenHeaderName))

	if err != nil {
		err = wrapChallengeToBattleError(errorPlayerNotOnline)
		utils.LogAndSendHTTPError(&w, err, http.StatusInternalServerError)
		return
	}

	battleLobby := ws.NewLobby(lobbyId)
	battle := NewBattle(battleLobby, config.DefaultCooldown, [2]string{authToken.Username, challengedPlayer})
	battle.addPlayer(authToken.Username, pokemonsForBattle, statsToken, trainerItems, conn, 0, r.Header.Get(tokens.AuthTokenHeaderName))
	hub.AwaitingLobbies.Store(lobbyId, battle)
	log.Infof("Created lobby: %s", battleLobby.Id.Hex())

	go func() {
		timer := time.NewTimer(time.Duration(config.BattleStartTimeout) * time.Second)
		<-timer.C
		if !battleLobby.Started {
			log.Error("Closing lobby because no player joined")
			ws.CloseLobby(battleLobby)
			hub.AwaitingLobbies.Delete(lobbyId)
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
		err := wrapAcceptChallengeError(ws.WrapUpgradeConnectionError(err))
		utils.LogAndSendHTTPError(&w, err, http.StatusInternalServerError)
		return
	}

	authToken, err := tokens.ExtractAndVerifyAuthToken(r.Header)

	if err != nil {
		// TODO Should refactor default messages and not build every time
		_ = conn.WriteMessage(websocket.TextMessage, []byte("No auth token"))
		_ = conn.Close()
		return
	}

	trainersClient := clients.NewTrainersClient(httpClient)
	trainerItems, statsToken, pokemonsForBattle, err := extractAndVerifyTokensForBattle(trainersClient, authToken.Username, r)

	if err != nil {
		log.Error(wrapAcceptChallengeError(err))
		// TODO use proper messages with constructors
		_ = conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
		_ = conn.Close()
		return
	}

	log.Infof("Player pokemons:")
	for _, p := range pokemonsForBattle {
		log.Infof("%s\t:\t%s", p.Id.Hex(), p.Species)
	}

	// TODO error not verified?
	lobbyId, err := primitive.ObjectIDFromHex(mux.Vars(r)[api.BattleIdPathVar])

	if err != nil {
		log.Error(wrapAcceptChallengeError(err))
		// TODO use proper messages with constructors
		_ = conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
		_ = conn.Close()
	}

	value, ok := hub.AwaitingLobbies.Load(lobbyId)
	battle := value.(valueType)

	if !ok {
		log.Error(wrapAcceptChallengeError(errorBattleDoesNotExist))
		_ = conn.WriteMessage(websocket.TextMessage, []byte(errorBattleDoesNotExist.Error()))
		_ = conn.Close()
		return
	}

	if battle.Expected[1] != authToken.Username {
		log.Error(wrapAcceptChallengeError(err))
		_ = conn.WriteMessage(websocket.TextMessage, []byte(errorPlayerUnauthorized.Error()))
		_ = conn.Close()
		return
	}

	hub.AwaitingLobbies.Delete(lobbyId)
	log.Infof("Trainer %s joined ws %s", authToken.Username, mux.Vars(r)[api.BattleIdPathVar])
	battle.addPlayer(authToken.Username, pokemonsForBattle, statsToken, trainerItems, conn, 1,
		r.Header.Get(tokens.AuthTokenHeaderName))
	startBattle(trainersClient, lobbyId, battle)
}

func HandleRejectChallenge(w http.ResponseWriter, r *http.Request) {
	authToken, err := tokens.ExtractAndVerifyAuthToken(r.Header)
	if err != nil {
		utils.LogAndSendHTTPError(&w, wrapRejectChallengeError(err), http.StatusUnauthorized)
		return
	}

	vars := mux.Vars(r)
	lobbyIdHex, ok := vars[api.BattleIdPathVar]
	if !ok {
		utils.LogAndSendHTTPError(&w, wrapRejectChallengeError(errorBattleDoesNotExist), http.StatusBadRequest)
		return
	}

	var battleInterface interface{}
	battleInterface, ok = hub.AwaitingLobbies.Load(lobbyIdHex)
	if !ok {
		utils.LogAndSendHTTPError(&w, wrapRejectChallengeError(errorBattleDoesNotExist), http.StatusBadRequest)
		return

	}

	battle := battleInterface.(valueType)
	for _, trainer := range battle.Expected {
		if trainer == authToken.Username {
			log.Infof("%s rejected invite for lobby %s", trainer, lobbyIdHex)
			ws.CloseLobby(battle.Lobby)
			hub.AwaitingLobbies.Delete(battle.Lobby.Id)
			return
		}
	}

	utils.LogAndSendHTTPError(&w, wrapRejectChallengeError(errorPlayerUnauthorized), http.StatusUnauthorized)
}

func startBattle(trainersClient *clients.TrainersClient, battleId primitive.ObjectID, battle *Battle) {
	defer log.Info("Cleaning lobby")
	defer hub.ongoingBattles.Delete(battleId)

	log.Infof("Battle %s starting...", battleId.Hex())
	hub.ongoingBattles.Store(battleId, battle)

	winner, err := battle.StartBattle()
	if err != nil {
		log.Error(err)
		ws.CloseLobby(battle.Lobby)
	} else {
		log.Infof("Battle %s finished, winner is: %s", battleId, winner)
		err := commitBattleResults(trainersClient, battleId.Hex(), battle)
		if err != nil {
			log.Error(err)
		}
		battle.FinishBattle()
	}

	// finish battle
	log.Warnf("Active goroutines: %d", runtime.NumGoroutine())
}

func extractAndVerifyTokensForBattle(trainersClient *clients.TrainersClient, username string,
	r *http.Request) (map[string]items.Item, *utils.TrainerStats, map[string]*pokemons.Pokemon, error) {
	pokemonTkns, err := tokens.ExtractAndVerifyPokemonTokens(r.Header)
	if err != nil {
		return nil, nil, nil, err
	}

	// pokemons

	if len(pokemonTkns) > config.PokemonsPerBattle {
		return nil, nil, nil, errorTooManyPokemons
	}

	if len(pokemonTkns) < config.PokemonsPerBattle {
		return nil, nil, nil, errorNotEnoughPokemons
	}

	pokemonsInToken := make(map[string]*pokemons.Pokemon, len(pokemonTkns))
	pokemonHashes := make(map[string][]byte, len(pokemonTkns))
	for _, pokemonTkn := range pokemonTkns {
		pokemonId := pokemonTkn.Pokemon.Id.Hex()
		pokemonsInToken[pokemonId] = &pokemonTkn.Pokemon
		pokemonHashes[pokemonId] = pokemonTkn.PokemonHash
	}

	authToken := r.Header.Get(tokens.AuthTokenHeaderName)

	valid, err := trainersClient.VerifyPokemons(username, pokemonHashes, authToken)
	if err != nil {
		return nil, nil, nil, err
	}

	if !*valid {
		return nil, nil, nil, tokens.ErrorInvalidPokemonTokens
	}

	// stats

	trainerStatsToken, err := tokens.ExtractAndVerifyTrainerStatsToken(r.Header)
	if err != nil {
		return nil, nil, nil, err
	}

	valid, err = trainersClient.VerifyTrainerStats(username, trainerStatsToken.TrainerHash, authToken)
	if err != nil {
		return nil, nil, nil, err
	}

	if !*valid {
		return nil, nil, nil, tokens.ErrorInvalidStatsToken
	}

	// items

	itemsToken, err := tokens.ExtractAndVerifyItemsToken(r.Header)
	if err != nil {
		return nil, nil, nil, err
	}

	valid, err = trainersClient.VerifyItems(username, itemsToken.ItemsHash, authToken)
	if err != nil {
		return nil, nil, nil, err
	}

	if !*valid {
		return nil, nil, nil, tokens.ErrorInvalidItemsToken
	}

	return itemsToken.Items, &trainerStatsToken.TrainerStats, pokemonsInToken, nil
}

func commitBattleResults(trainersClient *clients.TrainersClient, battleId string, battle *Battle) error {
	log.Infof("Committing battle results from battle %s, with winner: %s", battleId, battle.Winner)

	experienceGain := experience.GetPokemonExperienceGainFromBattle(battle.Winner == battle.PlayersBattleStatus[0].Username)
	err := UpdateTrainerPokemons(trainersClient, *battle.PlayersBattleStatus[0], battle.AuthTokens[0],
		*battle.Lobby.TrainerOutChannels[0], experienceGain)
	if err != nil {
		return wrapCommitResultsError(err, battleId)
	}

	experienceGain = experience.GetPokemonExperienceGainFromBattle(battle.Winner == battle.PlayersBattleStatus[1].Username)
	err = UpdateTrainerPokemons(trainersClient, *battle.PlayersBattleStatus[1], battle.AuthTokens[1], *battle.Lobby.TrainerOutChannels[1], experienceGain)
	if err != nil {
		return wrapCommitResultsError(err, battleId)
	}

	// Update trainer stats: add experience
	experienceGain = experience.GetTrainerExperienceGainFromBattle(battle.Winner == battle.PlayersBattleStatus[0].Username)
	err = AddExperienceToPlayer(trainersClient, *battle.PlayersBattleStatus[0], battle.AuthTokens[0], *battle.Lobby.TrainerOutChannels[0], experienceGain)
	if err != nil {
		return wrapCommitResultsError(err, battleId)
	}

	experienceGain = experience.GetTrainerExperienceGainFromBattle(battle.Winner == battle.PlayersBattleStatus[1].Username)
	err = AddExperienceToPlayer(trainersClient, *battle.PlayersBattleStatus[1], battle.AuthTokens[1], *battle.Lobby.TrainerOutChannels[1], experienceGain)
	if err != nil {
		return wrapCommitResultsError(err, battleId)
	}

	// Update trainer items, removing the items that were used during the battle
	err = RemoveUsedItems(trainersClient, *battle.PlayersBattleStatus[0], battle.AuthTokens[0], *battle.Lobby.TrainerOutChannels[0])
	if err != nil {
		return wrapCommitResultsError(err, battleId)
	}

	err = RemoveUsedItems(trainersClient, *battle.PlayersBattleStatus[1], battle.AuthTokens[1], *battle.Lobby.TrainerOutChannels[1])
	if err != nil {
		return wrapCommitResultsError(err, battleId)
	}

	return nil
}

func RemoveUsedItems(trainersClient *clients.TrainersClient, player battles.TrainerBattleStatus, authToken string,
	outChan chan ws.GenericMsg) error {

	usedItems := player.UsedItems
	if len(usedItems) == 0 {
		return nil
	}

	itemIds := make([]string, 0, len(usedItems))

	for itemId := range usedItems {
		itemIds = append(itemIds, itemId)
	}

	_, err := trainersClient.RemoveItems(player.Username, itemIds, authToken)
	if err != nil {
		return err
	}

	setTokensMessage := ws.SetTokenMessage{
		TokenField:   tokens.ItemsTokenHeaderName,
		TokensString: []string{trainersClient.ItemsToken},
	}.SerializeToWSMessage()
	outChan <- ws.GenericMsg{
		MsgType: websocket.TextMessage,
		Data:    []byte(setTokensMessage.Serialize()),
	}
	return nil
}

func UpdateTrainerPokemons(trainersClient *clients.TrainersClient, player battles.TrainerBattleStatus,
	authToken string, outChan chan ws.GenericMsg, xpAmount float64) error {

	// updates pokemon status after battle: adds XP and updates HP
	// player 0

	for id, pokemon := range player.TrainerPokemons {
		pokemon.XP += xpAmount
		pokemon.HP = pokemon.MaxHP

		_, err := trainersClient.UpdateTrainerPokemon(player.Username, id, *pokemon, authToken)
		if err != nil {
			return err
		}
	}

	toSend := make([]string, len(trainersClient.PokemonTokens))
	i := 0
	for _, v := range trainersClient.PokemonTokens {
		toSend[i] = v
		i++
	}

	setTokensMessage := ws.SetTokenMessage{
		TokenField:   tokens.PokemonsTokenHeaderName,
		TokensString: toSend,
	}.SerializeToWSMessage()
	outChan <- ws.GenericMsg{
		MsgType: websocket.TextMessage,
		Data:    []byte(setTokensMessage.Serialize()),
	}
	return nil
}

func AddExperienceToPlayer(trainersClient *clients.TrainersClient, player battles.TrainerBattleStatus,
	authToken string, outChan chan ws.GenericMsg, XPAmount float64) error {

	stats := player.TrainerStats
	stats.XP += XPAmount

	_, err := trainersClient.UpdateTrainerStats(player.Username, *stats, authToken)
	if err != nil {
		return err
	}

	setTokensMessage := ws.SetTokenMessage{
		TokenField:   tokens.StatsTokenHeaderName,
		TokensString: []string{trainersClient.TrainerStatsToken},
	}.SerializeToWSMessage()
	outChan <- ws.GenericMsg{
		MsgType: websocket.TextMessage,
		Data:    []byte(setTokensMessage.Serialize()),
	}

	return nil
}

func loadConfig() *BattleServerConfig {
	fileData, err := ioutil.ReadFile(configFilename)
	if err != nil {
		log.Fatal(err)
		return nil
	}

	var config BattleServerConfig
	err = json.Unmarshal(fileData, &config)
	if err != nil {
		log.Fatal(err)
		return nil
	}

	log.Infof("Loaded config: %+v", config)
	return &config
}
