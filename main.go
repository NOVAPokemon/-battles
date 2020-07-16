package main

import (
	"sync"

	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/clients"
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

	flags := utils.ParseFlags(serverName)

	if !*flags.LogToStdout {
		utils.SetLogFile(serverName)
	}

	if utils.CheckDelayedFlag(*flags.DelayedComms) {
		commsManager = utils.CreateDefaultCommunicationManager()
	} else {
		locationTag := utils.GetLocationTag(utils.DefaultLocationTagsFilename, serverName)
		commsManager = utils.CreateDelayedCommunicationManager(utils.DefaultDelayConfigFilename, locationTag)
	}

	hub = &battleHub{
		notificationClient: clients.NewNotificationClient(nil, commsManager),
		AwaitingLobbies:    sync.Map{},
		QueuedBattles:      sync.Map{},
		ongoingBattles:     sync.Map{},
	}

	utils.StartServer(serviceName, host, port, routes, commsManager)
}
