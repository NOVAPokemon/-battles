package main

import (
	"encoding/json"
	"fmt"
	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/api"
	"github.com/NOVAPokemon/utils/clients"
	"github.com/NOVAPokemon/utils/cookies"
	"github.com/NOVAPokemon/utils/notifications"
	ws "github.com/NOVAPokemon/utils/websockets"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"net/http"
	"strings"
)

const BattlesName = "Battles"

type BattleHub struct {
	notificationClient *clients.NotificationClient

	CreatedBattles map[primitive.ObjectID]*Battle
	QueuedBattles  map[primitive.ObjectID]*Battle
	ongoingBattles map[primitive.ObjectID]*Battle
}

var hub *BattleHub

func init() {
	hub = &BattleHub{
		notificationClient: clients.NewNotificationClient(fmt.Sprintf("%s:%d", utils.Host, utils.NotificationsPort), nil),
		CreatedBattles:     make(map[primitive.ObjectID]*Battle, 100),
		QueuedBattles:      make(map[primitive.ObjectID]*Battle, 100),
		ongoingBattles:     make(map[primitive.ObjectID]*Battle, 100),
	}
}

func HandleGetCurrentLobbies(w http.ResponseWriter, _ *http.Request) {

	var availableLobbies = make([]utils.Lobby, 0)

	for k, v := range hub.QueuedBattles {
		toAdd := utils.Lobby{
			Id:       k,
			Username: v.playerIds[0],
		}
		availableLobbies = append(availableLobbies, toAdd)
	}

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
		http.Error(w, "Connection Error", http.StatusInternalServerError)
		conn.Close()
		return
	}

	authToken, err := cookies.ExtractAndVerifyAuthToken(&w, r, BattlesName)

	if err != nil {
		http.Error(w, "Missing auth token", http.StatusUnauthorized)
		return
	}

	pokemonTkns := cookies.ExtractPokemonTokens(r)
	pokemons := make([]utils.Pokemon, len(pokemonTkns))
	for _, pokemonTkn := range pokemonTkns {
		log.Infof(pokemonTkn.Pokemon.Id.Hex())
		pokemons = append(pokemons, pokemonTkn.Pokemon)
	}

	if len(hub.QueuedBattles) > 0 {
		var battleId primitive.ObjectID
		var battle *Battle
		for k, v := range hub.QueuedBattles {
			battleId = k
			battle = v
			break
		}
		delete(hub.QueuedBattles, battleId)
		hub.ongoingBattles[battleId] = battle
		battle.addPlayer(authToken.Username, pokemons, conn, 1)
		startBattle(battleId, battle)
		return
	}

	lobbyId := primitive.NewObjectID()
	battleLobby := ws.NewLobby(lobbyId)
	battle := NewBattle(battleLobby)
	battle.addPlayer(authToken.Username, pokemons, conn, 0)
	hub.QueuedBattles[lobbyId] = battle
}

func HandleChallengeToBattle(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error(err)
		http.Error(w, "Connection Error", http.StatusInternalServerError)
		conn.Close()
		return
	}

	authToken, err := cookies.ExtractAndVerifyAuthToken(&w, r, BattlesName)

	if err != nil {
		return
	}

	challengedPlayer := mux.Vars(r)[api.TargetPlayerIdPathvar]

	lobbyId := primitive.NewObjectID()
	toSend := utils.Notification{
		Id:       primitive.NewObjectID(),
		Username: challengedPlayer,
		Type:     notifications.ChallengeToBattle,
		Content:  lobbyId.Hex(),
	}

	authCookie, _ := r.Cookie(cookies.AuthTokenCookieName)
	hub.notificationClient.Jar, err = clients.CreateJarWithCookies(authCookie)

	log.Infof("NOtificating other player...")

	err = hub.notificationClient.AddNotification(toSend)

	if err != nil {
		http.Error(w, "Challenged player not online", http.StatusInternalServerError)
	}

	pokemonTkns := cookies.ExtractPokemonTokens(r)
	pokemons := make([]utils.Pokemon, len(pokemonTkns))
	for _, pokemonTkn := range pokemonTkns {
		log.Infof(pokemonTkn.Pokemon.Id.Hex())
		pokemons = append(pokemons, pokemonTkn.Pokemon)
	}

	battleLobby := ws.NewLobby(lobbyId)
	ws.AddTrainer(battleLobby, authToken.Username, conn)

	battle := NewBattle(battleLobby)
	battle.addPlayer(authToken.Username, pokemons, conn, 0)
	hub.CreatedBattles[lobbyId] = battle
}

func HandleAcceptChallenge(w http.ResponseWriter, r *http.Request) {
	conn2, err := upgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Println(err)
		http.Error(w, "Connection Error", http.StatusInternalServerError)
		return
	}

	authToken, err := cookies.ExtractAndVerifyAuthToken(&w, r, BattlesName)

	if err != nil {
		http.Error(w, "Missing auth token", http.StatusUnauthorized)
		return
	}

	splitPath := strings.Split(r.URL.Path, "/")
	lobbyId, err := primitive.ObjectIDFromHex(splitPath[len(splitPath)-1])
	if err != nil {
		log.Println(err)
		http.Error(w, "Invalid battleId", http.StatusBadRequest)
		conn2.Close()
		return
	}

	battle, ok := hub.CreatedBattles[lobbyId]
	if !ok {
		log.Println(err)
		http.Error(w, "Battle missing", http.StatusNotFound)
		conn2.Close()
		return
	}
	delete(hub.CreatedBattles, lobbyId)

	log.Infof("Trainer %s created ws %s", authToken.Username, lobbyId.Hex())
	log.Info("Trainer Pokemons:")
	pokemonTkns := cookies.ExtractPokemonTokens(r)
	pokemons := make([]utils.Pokemon, len(pokemonTkns))
	for _, pokemonTkn := range pokemonTkns {
		log.Infof(pokemonTkn.Pokemon.Id.Hex())
		pokemons = append(pokemons, pokemonTkn.Pokemon)
	}

	ws.AddTrainer(battle.Lobby, authToken.Username, conn2)
	battle.addPlayer(authToken.Username, pokemons, conn2, 1)

	startBattle(lobbyId, battle)

}

func startBattle(battleId primitive.ObjectID, battle *Battle) {
	winner, err := battle.StartBattle()

	if err != nil {
		log.Error(err)
	} else {
		log.Infof("Battle %s finished, winner is: %s", battleId, winner)
		commitBattleResults(battleId.Hex(), battle)
	}

	ws.CloseLobby(battle.Lobby)
	delete(hub.ongoingBattles, battleId)

}

func commitBattleResults(battleId string, status *Battle) {
	log.Infof("Commiting battle results from battle %s, with winner: %s", battleId, status.Winner)
}
