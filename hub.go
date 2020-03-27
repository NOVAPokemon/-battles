package main

import (
	"encoding/json"
	"github.com/NOVAPokemon/authentication/auth"
	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/websockets"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"net/http"
	"strings"
)

const BattlesName = "Battles"

func HandleGetCurrentLobbies(hub *Hub, w http.ResponseWriter, r *http.Request) {

	var availableLobbies = make([]utils.Lobby, 0)

	for k, v := range hub.Battles {
		if !v.Started {
			if len(v.Trainers) == 1 {
				toAdd := utils.Lobby{
					Id:       k,
					Username: v.Trainers[0].Username,
				}
				availableLobbies = append(availableLobbies, toAdd)
			}
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

func HandleCreateBattleLobby(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error(err)
		http.Error(w, "Connection Error", http.StatusInternalServerError)
		conn.Close()
		return
	}

	err, claims := auth.VerifyJWT(&w, r, BattlesName)
	var trainer = claims.Trainer

	lobbyId := primitive.NewObjectID()

	log.Infof("Trainer %s created lobby %s", trainer.Username, lobbyId.Hex())
	log.Info("Trainer Pokemons:")
	for _, pokemon := range trainer.Pokemons {
		log.Infof(pokemon.Id.Hex())
	}

	lobby := websockets.NewLobby(lobbyId)
	websockets.AddTrainer(lobby, trainer, conn)
	hub.Battles[lobbyId] = lobby
}

func HandleJoinBattleLobby(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn2, err := upgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Println(err)
		http.Error(w, "Connection Error", http.StatusInternalServerError)
		return
	}

	err, claims := auth.VerifyJWT(&w, r, BattlesName)
	var trainer = claims.Trainer

	splitPath := strings.Split(r.URL.Path, "/")
	lobbyId, err := primitive.ObjectIDFromHex(splitPath[len(splitPath)-1])

	if err != nil {
		log.Println(err)
		http.Error(w, "Invalid battleId", http.StatusBadRequest)
		conn2.Close()
		return
	}

	lobby := hub.Battles[lobbyId]

	if lobby == nil {
		log.Println(err)
		http.Error(w, "Battle missing", http.StatusNotFound)
		conn2.Close()
		return
	}

	log.Infof("Trainer %s created lobby %s", trainer.Username, lobbyId.Hex())
	log.Info("Trainer Pokemons:")
	for _, pokemon := range trainer.Pokemons {
		log.Infof(pokemon.Id.Hex())
	}

	websockets.AddTrainer(lobby, trainer, conn2)

	result, err := StartBattle(lobby)

	if err != nil {
		log.Error(err)
	} else {
		log.Infof("Battle %s finished, winner is: %s", lobby.Id.Hex(), result.Winner.Username)
		commitBattleResults(result)
		websockets.CloseLobby(lobby)
	}
}

func commitBattleResults(status *BattleStatus) {

	log.Infof("Commiting battle results")

}
