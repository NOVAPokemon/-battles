package main

import (
	"os"
	"sync"

	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/clients"
	"github.com/golang/geo/s2"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
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
	flags := utils.ParseFlags(serverName)

	if !*flags.LogToStdout {
		utils.SetLogFile(serverName)
	}

	location, exists := os.LookupEnv("LOCATION")
	if !exists {
		log.Fatal("no location in environment")
	}

	cellID := s2.CellIDFromToken(location)

	if !*flags.DelayedComms {
		commsManager = utils.CreateDefaultCommunicationManager()
	} else {
		commsManager = utils.CreateDefaultDelayedManager(false, &utils.OptionalConfigs{CellID: cellID})
	}

	hub = &battleHub{
		notificationClient: clients.NewNotificationClient(nil, commsManager, httpClient, basicClient),
		AwaitingLobbies:    sync.Map{},
		QueuedBattles:      sync.Map{},
		ongoingBattles:     sync.Map{},
	}

	recordMetrics()

	log.Print("ola")

	utils.StartServer(serviceName, host, port, routes, commsManager)
}
