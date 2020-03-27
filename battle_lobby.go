package main

import (
	"github.com/NOVAPokemon/utils"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// BattleLobby maintains the connections from both trainers and the status of the battle
type BattleLobby struct {
	Id primitive.ObjectID

	Trainer1 utils.Trainer
	Trainer2 utils.Trainer

	trainer1Conn *websocket.Conn
	trainer2Conn *websocket.Conn

	trainer1ChanIn  chan *string
	trainer1ChanOut chan *string

	trainer2ChanIn  chan *string
	trainer2ChanOut chan *string

	started  bool
	finished bool
}

func NewBattle(id primitive.ObjectID, trainer1 utils.Trainer, trainer1Conn *websocket.Conn) *BattleLobby {

	trainer1ChanIn := make(chan *string)
	trainer1ChanOut := make(chan *string)

	go handleRecv(trainer1Conn, trainer1ChanIn)
	go handleSend(trainer1Conn, trainer1ChanOut)

	return &BattleLobby{
		Id:              id,
		Trainer1:        trainer1,
		trainer1Conn:    trainer1Conn,
		trainer1ChanIn:  trainer1ChanIn,
		trainer1ChanOut: trainer1ChanOut,
		started:         false,
		finished:        false,
	}
}

func JoinBattle(lobby *BattleLobby, trainer2 utils.Trainer, trainer2Conn *websocket.Conn) {

	trainer2ChanIn := make(chan *string)
	trainer2ChanOut := make(chan *string)

	lobby.Trainer2 = trainer2
	lobby.trainer2Conn = trainer2Conn
	lobby.trainer2ChanIn = trainer2ChanIn
	lobby.trainer2ChanOut = trainer2ChanOut

	go handleRecv(lobby.trainer2Conn, trainer2ChanIn)
	go handleSend(lobby.trainer2Conn, trainer2ChanOut)
}

func StartBattle(lobby *BattleLobby) {

	// start battle logic, which consists in handling
	// the messages (trainer commands such as "attack") piped from the channels
	// then, process the commands and pipe messages into the outbound pipes
	lobby.started = true

	log.Infof("Started Battle")

	for {
		select {
		case msg := <-lobby.trainer1ChanIn:
			log.Infof("[Lobby %s]: Message from trainer 1 received: %s", lobby.Id.Hex(), *msg)
			lobby.trainer2ChanOut <- msg

		case msg := <-lobby.trainer2ChanIn:
			log.Infof("[Lobby %s]: Message from trainer 2 received: %s", lobby.Id.Hex(), *msg)
			lobby.trainer1ChanOut <- msg
		}
	}
	// handleFinishBattle()
}

func handleSend(conn *websocket.Conn, channel chan *string) {

	for {
		msg := <-channel

		err := conn.WriteMessage(websocket.TextMessage, []byte(*msg))

		if err != nil {
			log.Error("write err:", err)
		} else {
			log.Debugf("Wrote %s into the channel", *msg)
		}
	}
}

func handleRecv(conn *websocket.Conn, channel chan *string) {

	for {
		_, message, err := conn.ReadMessage()

		if err != nil {
			log.Println(err)
		} else {
			msg := string(message)
			log.Infof("Message received: %s", msg)
			channel <- &msg
		}

	}

}
