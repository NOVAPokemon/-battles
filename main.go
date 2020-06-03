package main

import (
	"github.com/NOVAPokemon/utils"
	"github.com/gorilla/websocket"
)

const (
	host        = utils.ServeHost
	port        = utils.BattlesPort
	serviceName = "BATTLES"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func main() {
	recordMetrics()
	utils.CheckLogFlag(serviceName)
	utils.StartServer(serviceName, host, port, routes)
}
