package main

import (
	"github.com/NOVAPokemon/utils/websockets"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"net/http"
)

type Hub struct {
	Battles map[primitive.ObjectID]*websockets.Lobby
}

var hub = &Hub{
	Battles: make(map[primitive.ObjectID]*websockets.Lobby),
}

func GetCurrentLobbies(w http.ResponseWriter, r *http.Request) {
	HandleGetCurrentLobbies(hub, w, r)
}

func CreateBattleLobby(w http.ResponseWriter, r *http.Request) {
	HandleCreateBattleLobby(hub, w, r)
}

func JoinBattleLobby(w http.ResponseWriter, r *http.Request) {
	HandleJoinBattleLobby(hub, w, r)
}
