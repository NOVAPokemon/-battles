package main

import (
	"encoding/json"
	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/clients"
	"github.com/NOVAPokemon/utils/cookies"
	ws "github.com/NOVAPokemon/utils/websockets"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"net/http"
	"strings"
)

const BattlesName = "Battles"

type battleHub struct {
	notificationClient clients.NotificationClient
	Battles            map[primitive.ObjectID]*Battle
	lobbies            map[primitive.ObjectID]*ws.Lobby
}

var hub *battleHub

func init() {
	hub = &battleHub{
		Battles: make(map[primitive.ObjectID]*Battle, 100),
		lobbies: make(map[primitive.ObjectID]*ws.Lobby, 100),
	}
}

func HandleGetCurrentLobbies(w http.ResponseWriter, _ *http.Request) {

	var availableLobbies = make([]utils.Lobby, 0)

	for k, v := range hub.lobbies {
		if !v.Started {
			toAdd := utils.Lobby{
				Id:       k,
				Username: v.TrainerUsernames[0],
			}
			availableLobbies = append(availableLobbies, toAdd)
		}
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

func HandleCreateBattleLobby(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error(err)
		http.Error(w, "Connection Error", http.StatusInternalServerError)
		conn.Close()
		return
	}

	authToken, err := cookies.ExtractAndVerifyAuthToken(&w, r, "battles")

	if err != nil {
		http.Error(w, "Missing auth token", http.StatusUnauthorized)
		return
	}

	lobbyId := primitive.NewObjectID()

	pokemonTkns := cookies.ExtractPokemonTokens(r)
	pokemons := make([]utils.Pokemon, len(pokemonTkns))
	for _, pokemonTkn := range pokemonTkns {
		log.Infof(pokemonTkn.Pokemon.Id.Hex())
		pokemons = append(pokemons, pokemonTkn.Pokemon)
	}

	battleLobby := ws.NewLobby(lobbyId)
	ws.AddTrainer(battleLobby, authToken.Username, conn)

	battle := NewBattle(battleLobby)
	battle.addPlayer(authToken.Username, pokemons, 0)

	hub.Battles[lobbyId] = battle
	hub.lobbies[lobbyId] = battleLobby
}

func HandleJoinBattleLobby(w http.ResponseWriter, r *http.Request) {
	conn2, err := upgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Println(err)
		http.Error(w, "Connection Error", http.StatusInternalServerError)
		return
	}

	authToken, err := cookies.ExtractAndVerifyAuthToken(&w, r, "battles")

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

	battleLobby, ok := hub.lobbies[lobbyId]
	if !ok {
		log.Println(err)
		http.Error(w, "Battle missing", http.StatusNotFound)
		conn2.Close()
		return
	}

	log.Infof("Trainer %s created ws %s", authToken.Username, lobbyId.Hex())
	log.Info("Trainer Pokemons:")
	pokemonTkns := cookies.ExtractPokemonTokens(r)
	pokemons := make([]utils.Pokemon, len(pokemonTkns))
	for _, pokemonTkn := range pokemonTkns {
		log.Infof(pokemonTkn.Pokemon.Id.Hex())
		pokemons = append(pokemons, pokemonTkn.Pokemon)
	}

	ws.AddTrainer(battleLobby, authToken.Username, conn2)
	battle, ok := hub.Battles[lobbyId]
	battle.addPlayer(authToken.Username, pokemons, 1)
	winner, err := battle.StartBattle()

	if err != nil {
		log.Error(err)
	} else {
		log.Infof("Battle %s finished, winner is: %s", battleLobby.Id.Hex(), winner)
		commitBattleResults(lobbyId.Hex(), battle)
		ws.CloseLobby(battleLobby)
	}
}

func commitBattleResults(battleId string, status *Battle) {

	log.Infof("Commiting battle results from battle %s", battleId)

}
