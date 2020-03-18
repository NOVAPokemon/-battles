package main

import (
	"encoding/json"
	"github.com/NOVAPokemon/utils"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"net/http"
	"strings"
)

type Hub struct {
	Battles map[primitive.ObjectID]*BattleLobby
}

func HandleGetCurrentLobbies(hub *Hub, w http.ResponseWriter, r *http.Request) {

	var availableLobbies = make([]utils.Lobby, 0)

	for k, v := range hub.Battles {
		if !v.started {
			toAdd := utils.Lobby{
				Id:        k,
				TrainerId: v.Trainer1.Id,
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

func HandleCreateBattleLobby(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error(err)
		http.Error(w, "Connection Error", http.StatusInternalServerError)
		conn.Close()
		return
	}

	// TODO change this to real auth
	//Trainer1Id := decodeJwtToken(r)
	//err, trainer1 := trainer.GetTrainerById(Trainer1Id)
	//if err != nil {
	//	log.Println(err)
	//	return err, hub
	//}

	trainer1 := utils.Trainer{}

	lobbyId := primitive.NewObjectID()
	lobby := NewBattle(lobbyId, trainer1, conn)
	hub.Battles[lobbyId] = lobby
}

func HandleJoinBattleLobby(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn2, err := upgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Println(err)
		http.Error(w, "Connection Error", http.StatusInternalServerError)
		return
	}

	// TODO change this to real auth
	//Trainer1Id := decodeJwtToken(r)
	//err, trainer1 := trainer.GetTrainerById(Trainer1Id)
	//if err != nil {
	//	log.Println(err)
	//	return err, hub
	//}

	splitPath := strings.Split(r.URL.Path, "/")
	lobbyId, err := primitive.ObjectIDFromHex(splitPath[len(splitPath)-1])

	if err != nil {
		log.Println(err)
		http.Error(w, "battleId invalid", http.StatusBadRequest)
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

	trainer2 := utils.Trainer{} //

	JoinBattle(lobby, trainer2, conn2)

	StartBattle(lobby)
}
