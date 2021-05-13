package main

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/NOVAPokemon/utils/notifications"
	notificationsMessages "github.com/NOVAPokemon/utils/websockets/notifications"
	"github.com/pkg/errors"

	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/api"
	"github.com/NOVAPokemon/utils/clients"
	"github.com/NOVAPokemon/utils/experience"
	"github.com/NOVAPokemon/utils/items"
	"github.com/NOVAPokemon/utils/pokemons"
	"github.com/NOVAPokemon/utils/tokens"
	ws "github.com/NOVAPokemon/utils/websockets"
	"github.com/NOVAPokemon/utils/websockets/battles"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type (
	keyType   = string
	valueType = *battleLobby
)

type battleHub struct {
	notificationClient *clients.NotificationClient
	AwaitingLobbies    sync.Map
	QueuedBattles      sync.Map
	ongoingBattles     sync.Map
}

const configFilename = "configs.json"

var (
	hub        *battleHub
	httpClient = &http.Client{
		Timeout:   ws.Timeout,
		Transport: clients.NewTransport(),
	}

	basicClient = clients.NewBasicClient(false, "")

	config *battleServerConfig

	serverName          string
	serviceNameHeadless string

	commsManager ws.CommunicationManager
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
}

func handleGetCurrentLobbies(w http.ResponseWriter, _ *http.Request) {
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

func handleQueueForBattle(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		err = wrapQueueBattleError(ws.WrapUpgradeConnectionError(err))
		utils.LogAndSendHTTPError(&w, err, http.StatusInternalServerError)
		return
	}

	authToken, err := tokens.ExtractAndVerifyAuthToken(r.Header)
	if err != nil {
		err = writeErrorMessageAndClose(conn, err)
		if err != nil {
			log.Error(err)
		}
		return
	}

	trackInfo := ws.GetTrackInfoFromHeader(&r.Header)

	trainersClient := clients.NewTrainersClient(httpClient, commsManager, basicClient)

	reqLogger := log.WithFields(log.Fields{
		"TYPE": "QUEUE",
		"USER": authToken.Username,
	})

	defer reqLogger.Info("exiting handler")

	reqLogger.Infof("New player queued for battle: %s", authToken.Username)
	trainerItems, statsToken, pokemonsForBattle, err := extractAndVerifyTokensForBattle(trainersClient,
		authToken.Username, r)
	if err != nil {
		err = writeErrorMessageAndClose(conn, err)
		if err != nil {
			reqLogger.Error(err)
		}
		return
	}

	reqLogger.Info("Player pokemons:")
	for _, p := range pokemonsForBattle {
		reqLogger.Infof("%s\t:\t%s", p.Id, p.Species)
	}

	found := false
	var (
		battleAux *battleLobby
		playerNr  int
	)
	hub.QueuedBattles.Range(func(key, value interface{}) bool {
		battleAux = value.(valueType)
		reqLogger.Infof("will try to add player to battle %s", battleAux.Lobby.Id)
		playerNr, err = battleAux.addPlayer(authToken.Username, pokemonsForBattle, statsToken, trainerItems,
			conn, r.Header.Get(tokens.AuthTokenHeaderName), commsManager)
		if err != nil {
			if errors.Cause(err) == ws.ErrorLobbyAlreadyFinished || errors.Cause(err) == ws.ErrorLobbyIsFull {
				reqLogger.Warn(err)
			} else {
				reqLogger.Error(err)
			}
			return true
		}
		if playerNr == 2 {
			reqLogger.Infof("Found battle %s", battleAux.Lobby.Id)
			found = true
			startBattle(trainersClient, battleAux.Lobby.Id, battleAux)
			return false
		}
		reqLogger.Infof("got into the weird possibility")
		return true
	})

	reqLogger.Info("iterated through queued battles")

	if found {
		reqLogger.Info("found a viable lobby to join")
		hub.QueuedBattles.Delete(battleAux.Lobby.Id)
		return
	}

	lobbyId := primitive.NewObjectID()

	reqLogger.Info("will create new lobby")
	battle := ws.NewLobby(lobbyId.Hex(), 2, &trackInfo)

	reqLogger.Info("will create battle")
	battleAux = createBattle(battle, config.DefaultCooldown, [2]string{authToken.Username, ""})
	reqLogger.Infof("Created lobby %s", battle.Id)

	_, err = battleAux.addPlayer(authToken.Username, pokemonsForBattle, statsToken, trainerItems, conn,
		r.Header.Get(tokens.AuthTokenHeaderName), commsManager)
	if err != nil {
		if errors.Cause(err) == ws.ErrorLobbyAlreadyFinished {
			reqLogger.Warn(err)
		} else {
			reqLogger.Error(err)
		}

		errMsg := ws.ErrorMessage{
			Info:  err.Error(),
			Fatal: true,
		}.ConvertToWSMessage()

		_ = commsManager.WriteGenericMessageToConn(conn, errMsg)
		_ = conn.Close()
		return
	}
	hub.QueuedBattles.Store(lobbyId, battleAux)
	go cleanBattle(trackInfo, battleAux, &hub.QueuedBattles)
}

func handleChallengeToBattle(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		err = wrapChallengeToBattleError(ws.WrapUpgradeConnectionError(err))
		utils.LogAndSendHTTPError(&w, err, http.StatusInternalServerError)
		return
	}

	authToken, err := tokens.ExtractAndVerifyAuthToken(r.Header)
	if err != nil {
		err = writeErrorMessageAndClose(conn, err)
		if err != nil {
			log.Error(err)
		}
		return
	}

	trackInfo := ws.GetTrackInfoFromHeader(&r.Header)

	trainersClient := clients.NewTrainersClient(httpClient, commsManager, basicClient)
	trainerItems, statsToken, pokemonsForBattle, err := extractAndVerifyTokensForBattle(trainersClient,
		authToken.Username, r)
	if err != nil {
		log.Error(wrapChallengeToBattleError(err))
		err = writeErrorMessageAndClose(conn, err)
		if err != nil {
			log.Error(err)
		}

		return
	}

	log.Infof("Player pokemons:")
	for _, p := range pokemonsForBattle {
		log.Infof("%s\t:\t%s", p.Id, p.Species)
	}

	challengedPlayer := mux.Vars(r)[api.TargetPlayerIdPathvar]
	log.Infof("Player %s challenged %s for a battle", authToken.Username, challengedPlayer)

	lobbyId := primitive.NewObjectID()
	battle := ws.NewLobby(lobbyId.Hex(), 2, &trackInfo)
	log.Infof("Created lobby: %s", battle.Id)
	newBattle := createBattle(battle, config.DefaultCooldown, [2]string{authToken.Username, challengedPlayer})
	_, err = newBattle.addPlayer(authToken.Username, pokemonsForBattle, statsToken, trainerItems, conn,
		r.Header.Get(tokens.AuthTokenHeaderName), commsManager)
	if err != nil {
		if errors.Cause(err) == ws.ErrorLobbyAlreadyFinished {
			log.Warn(wrapChallengeToBattleError(err))
		} else {
			log.Error(wrapChallengeToBattleError(err))
		}
		return
	}

	hub.AwaitingLobbies.Store(lobbyId, newBattle)
	go cleanBattle(trackInfo, newBattle, &hub.AwaitingLobbies)

	toMarshal := notifications.WantsToBattleContent{
		Username:       challengedPlayer,
		LobbyId:        lobbyId.Hex(),
		ServerHostname: serverName,
	}

	contentBytes, err := json.Marshal(toMarshal)
	if err != nil {
		log.Error(wrapChallengeToBattleError(err))
		return
	}
	notification := utils.Notification{
		Id:       primitive.NewObjectID().Hex(),
		Username: challengedPlayer,
		Type:     notifications.ChallengeToBattle,
		Content:  string(contentBytes),
	}

	notificationMsg := notificationsMessages.NotificationMessage{
		Notification: notification,
		Info:         trackInfo,
	}

	log.Infof("Sending notification: Id:%s Content:%s to %s", notification.Id,
		string(notification.Content), notification.Username)
	err = hub.notificationClient.AddNotification(&notificationMsg, r.Header.Get(tokens.AuthTokenHeaderName))

	if err != nil {
		err = wrapChallengeToBattleError(errorPlayerNotOnline)
		utils.LogAndSendHTTPError(&w, err, http.StatusInternalServerError)
		return
	}
}

func handleAcceptChallenge(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		err = wrapAcceptChallengeError(ws.WrapUpgradeConnectionError(err))
		utils.LogAndSendHTTPError(&w, err, http.StatusInternalServerError)
		return
	}

	authToken, err := tokens.ExtractAndVerifyAuthToken(r.Header)
	if err != nil {
		log.Error(wrapAcceptChallengeError(err))
		err = writeErrorMessageAndClose(conn, err)
		if err != nil {
			log.Error(err)
		}
		return
	}

	trainersClient := clients.NewTrainersClient(httpClient, commsManager, basicClient)
	trainerItems, statsToken, pokemonsForBattle, err := extractAndVerifyTokensForBattle(trainersClient,
		authToken.Username, r)
	if err != nil {
		log.Error(wrapAcceptChallengeError(err))
		err = writeErrorMessageAndClose(conn, err)
		if err != nil {
			log.Error(err)
		}
		return
	}

	log.Infof("Player pokemons:")
	for _, p := range pokemonsForBattle {
		log.Infof("%s\t:\t%s", p.Id, p.Species)
	}

	lobbyId, err := primitive.ObjectIDFromHex(mux.Vars(r)[api.BattleIdPathVar])
	if err != nil {
		log.Error(wrapAcceptChallengeError(err))
		err = writeErrorMessageAndClose(conn, err)
		if err != nil {
			log.Error(err)
		}
		return
	}

	value, ok := hub.AwaitingLobbies.Load(lobbyId)
	if !ok {
		log.Warn(wrapAcceptChallengeError(errorBattleDoesNotExist))
		err = writeErrorMessageAndClose(conn, errorBattleDoesNotExist)
		if err != nil {
			log.Error(err)
		}
		return
	}

	battle := value.(valueType)
	if battle.Expected[1] != authToken.Username {
		log.Error(wrapAcceptChallengeError(err))
		err = writeErrorMessageAndClose(conn, errorPlayerUnauthorized)
		if err != nil {
			log.Error(err)
		}
		return
	}

	log.Infof("Trainer %s joined ws %s", authToken.Username, mux.Vars(r)[api.BattleIdPathVar])
	playerNr, err := battle.addPlayer(authToken.Username, pokemonsForBattle, statsToken, trainerItems, conn,
		r.Header.Get(tokens.AuthTokenHeaderName), commsManager)
	if err != nil {
		err = writeErrorMessageAndClose(conn, err)
		if err != nil {
			log.Error(err)
		}
		return
	}

	if playerNr == 2 {
		startBattle(trainersClient, lobbyId.Hex(), battle)
		hub.AwaitingLobbies.Delete(lobbyId)
	}
}

func handleRejectChallenge(w http.ResponseWriter, r *http.Request) {
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

	lobbyId, err := primitive.ObjectIDFromHex(lobbyIdHex)
	if err != nil {
		utils.LogAndSendHTTPError(&w, wrapRejectChallengeError(errorBattleDoesNotExist), http.StatusInternalServerError)
		return
	}

	var battleInterface interface{}
	battleInterface, ok = hub.AwaitingLobbies.Load(lobbyId)
	if !ok {
		utils.LogWarnAndSendHTTPError(&w, wrapRejectChallengeError(errorBattleDoesNotExist), http.StatusNotFound)
		return

	}

	battle := battleInterface.(valueType)
	for _, trainer := range battle.Expected {
		if trainer == authToken.Username {
			log.Infof("%s rejected invite for lobby %s", trainer, lobbyIdHex)
			battle.Reject.Do(func() {
				close(battle.rejectChannel)
			})
			return
		}
	}

	utils.LogAndSendHTTPError(&w, wrapRejectChallengeError(errorPlayerUnauthorized), http.StatusUnauthorized)
}

func startBattle(trainersClient *clients.TrainersClient, battleId string, battle *battleLobby) {
	log.Infof("Battle %s starting...", battleId)
	hub.ongoingBattles.Store(battleId, battle)
	emitStartBattle()
	winner, err := battle.startBattle()
	if err != nil {
		log.Warn(err)
		ws.FinishLobby(battle.Lobby) // abort lobby without commiting
	} else {
		log.Infof("Battle %s finished, winner is: %s", battleId, winner)
		err = commitBattleResults(trainersClient, battleId, battle)
		if err != nil {
			log.Error(err)
		}
		battle.finishBattle() // wait for graceful finish
	}
	emitEndBattle()
	hub.ongoingBattles.Delete(battleId)
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
	pokemonHashes := make(map[string]string, len(pokemonTkns))
	for _, pokemonTkn := range pokemonTkns {
		pokemonId := pokemonTkn.Pokemon.Id
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

func commitBattleResults(trainersClient *clients.TrainersClient, battleId string, battle *battleLobby) error {
	log.Infof("Committing battle results from battle %s, with winner: %s", battleId, battle.Winner)

	experienceGain := experience.GetPokemonExperienceGainFromBattle(battle.Winner ==
		battle.PlayersBattleStatus[0].Username)
	err := updateTrainerPokemons(trainersClient, *battle.PlayersBattleStatus[0], battle.AuthTokens[0],
		battle.Lobby.TrainerOutChannels[0], experienceGain)
	if err != nil {
		return wrapCommitResultsError(err, battleId)
	}

	experienceGain = experience.GetPokemonExperienceGainFromBattle(battle.Winner ==
		battle.PlayersBattleStatus[1].Username)
	err = updateTrainerPokemons(trainersClient, *battle.PlayersBattleStatus[1], battle.AuthTokens[1],
		battle.Lobby.TrainerOutChannels[1], experienceGain)
	if err != nil {
		return wrapCommitResultsError(err, battleId)
	}

	// Update trainer stats: add experience
	experienceGain = experience.GetTrainerExperienceGainFromBattle(battle.Winner ==
		battle.PlayersBattleStatus[0].Username)
	err = addExperienceToPlayer(trainersClient, *battle.PlayersBattleStatus[0], battle.AuthTokens[0],
		battle.Lobby.TrainerOutChannels[0], experienceGain)
	if err != nil {
		return wrapCommitResultsError(err, battleId)
	}

	experienceGain = experience.GetTrainerExperienceGainFromBattle(battle.Winner ==
		battle.PlayersBattleStatus[1].Username)
	err = addExperienceToPlayer(trainersClient, *battle.PlayersBattleStatus[1], battle.AuthTokens[1],
		battle.Lobby.TrainerOutChannels[1], experienceGain)
	if err != nil {
		return wrapCommitResultsError(err, battleId)
	}

	// Update trainer items, removing the items that were used during the battle
	err = removeUsedItems(trainersClient, *battle.PlayersBattleStatus[0], battle.AuthTokens[0],
		battle.Lobby.TrainerOutChannels[0])
	if err != nil {
		return wrapCommitResultsError(err, battleId)
	}

	err = removeUsedItems(trainersClient, *battle.PlayersBattleStatus[1], battle.AuthTokens[1],
		battle.Lobby.TrainerOutChannels[1])
	if err != nil {
		return wrapCommitResultsError(err, battleId)
	}

	return nil
}

func removeUsedItems(trainersClient *clients.TrainersClient, player battles.TrainerBattleStatus, authToken string,
	outChan chan *ws.WebsocketMsg) error {
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
	}

	outChan <- setTokensMessage.ConvertToWSMessage()

	return nil
}

func updateTrainerPokemons(trainersClient *clients.TrainersClient, player battles.TrainerBattleStatus,
	authToken string, outChan chan *ws.WebsocketMsg, xpAmount float64) error {
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
	}

	outChan <- setTokensMessage.ConvertToWSMessage()
	return nil
}

func cleanBattle(info ws.TrackedInfo, battle *battleLobby, containingMap *sync.Map) {
	timer := time.NewTimer(time.Duration(config.BattleStartTimeout) * time.Second)
	defer timer.Stop()
	defer containingMap.Delete(battle.Lobby.Id)
	select {
	case <-timer.C:
		log.Warnf("closing lobby %s since time expired", battle.Lobby.Id)
		ws.FinishLobby(battle.Lobby)
	case <-battle.rejectChannel:
		if ws.GetTrainersJoined(battle.Lobby) > 0 {
			select {
			case <-battle.Lobby.DoneListeningFromConn[0]:
			default:
				select {
				case battle.Lobby.TrainerOutChannels[0] <- battles.RejectBattleMessage{}.ConvertToWSMessage(info):
				default:
				}
			}
		}
		ws.FinishLobby(battle.Lobby)
	case <-battle.Lobby.Started:
	}
}

func addExperienceToPlayer(trainersClient *clients.TrainersClient, player battles.TrainerBattleStatus,
	authToken string, outChan chan *ws.WebsocketMsg, XPAmount float64) error {
	stats := player.TrainerStats
	stats.XP += XPAmount

	_, err := trainersClient.UpdateTrainerStats(player.Username, *stats, authToken)
	if err != nil {
		return err
	}

	setTokensMessage := ws.SetTokenMessage{
		TokenField:   tokens.StatsTokenHeaderName,
		TokensString: []string{trainersClient.TrainerStatsToken},
	}

	outChan <- setTokensMessage.ConvertToWSMessage()

	return nil
}

func loadConfig() *battleServerConfig {
	fileData, err := ioutil.ReadFile(configFilename)
	if err != nil {
		log.Fatal(err)
		return nil
	}

	var configAux battleServerConfig
	err = json.Unmarshal(fileData, &configAux)
	if err != nil {
		log.Fatal(err)
		return nil
	}

	log.Infof("Loaded config: %+v", configAux)
	return &configAux
}

func writeErrorMessageAndClose(conn *websocket.Conn, msgErr error) error {
	errMsg := ws.ErrorMessage{
		Info:  msgErr.Error(),
		Fatal: false,
	}

	err := commsManager.WriteGenericMessageToConn(conn, errMsg.ConvertToWSMessage())
	if err != nil {
		return ws.WrapWritingMessageError(err)
	}

	err = conn.Close()
	if err != nil {
		return ws.WrapClosingConnectionError(err)
	}

	return nil
}
