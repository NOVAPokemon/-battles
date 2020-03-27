package main

import (
	"fmt"
	"github.com/NOVAPokemon/utils"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
	"math/rand"
	"net/http"
	"time"
)

const host = "localhost"
const Port = 8002

var addr = fmt.Sprintf("%s:%d", host, Port)
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func main() {

	rand.Seed(time.Now().Unix())
	r := utils.NewRouter(routes)
	log.Infof("Starting BATTLES server in port %d...\n", Port)
	log.Fatal(http.ListenAndServe(addr, r))

}
