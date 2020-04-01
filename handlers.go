package main

import (
	"net/http"
)

func GetCurrentLobbies(w http.ResponseWriter, r *http.Request) {
	HandleGetCurrentLobbies(w, r)
}

func CreateBattleLobby(w http.ResponseWriter, r *http.Request) {
	HandleCreateBattleLobby(w, r)
}

func JoinBattleLobby(w http.ResponseWriter, r *http.Request) {
	HandleJoinBattleLobby(w, r)
}

func QueueForBattle(w http.ResponseWriter, r *http.Request) {
	HandleQueueForBattle(w, r)
}
