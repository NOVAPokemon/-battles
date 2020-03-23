package main

import (
	"flag"
	"fmt"
	"github.com/NOVAPokemon/utils/websockets"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"math/rand"
	"net/http"
	"time"
)

var url = ":8002"
var Addr = flag.String("addr", url, "http service address")

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func main() {

	fmt.Print()
	rand.Seed(time.Now().Unix())
	flag.Parse()

	hub := &Hub{
		Battles: make(map[primitive.ObjectID]*websockets.Lobby),
	}

	r := mux.NewRouter()
	r.HandleFunc("/battles", func(w http.ResponseWriter, r *http.Request) {
		HandleGetCurrentLobbies(hub, w, r)
	})
	r.HandleFunc("/battles/join", func(w http.ResponseWriter, r *http.Request) {
		HandleCreateBattleLobby(hub, w, r)
	})
	r.HandleFunc("/battles/join/{battleId}", func(w http.ResponseWriter, r *http.Request) {
		HandleJoinBattleLobby(hub, w, r)
	})

	log.Infof("Starting BATTLES server on %s...\n", url)
	log.Fatal(http.ListenAndServe(*Addr, r))

}
