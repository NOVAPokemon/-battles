package main

import (
	"log"
	"os"
	"sync"

	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/clients"
	http "github.com/bruno-anjos/archimedesHTTPClient"
	"github.com/golang/geo/s2"
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
	flags := utils.ParseFlags(serverName)

	if !*flags.LogToStdout {
		utils.SetLogFile(serverName)
	}

	if !*flags.DelayedComms {
		commsManager = utils.CreateDefaultCommunicationManager()
	} else {
		locationTag := utils.GetLocationTag(utils.DefaultLocationTagsFilename, serverName)
		commsManager = utils.CreateDefaultDelayedManager(locationTag, false)
	}

	location, exists := os.LookupEnv("LOCATION")
	if !exists {
		log.Fatalf("no location in environment")
	}

	httpClient.InitArchimedesClient("localhost", http.DefaultArchimedesPort, s2.CellIDFromToken(location).LatLng())

	hub = &battleHub{
		notificationClient: clients.NewNotificationClient(nil, commsManager, httpClient),
		AwaitingLobbies:    sync.Map{},
		QueuedBattles:      sync.Map{},
		ongoingBattles:     sync.Map{},
	}

	recordMetrics()

	utils.StartServer(serviceName, host, port, routes, commsManager)
}
