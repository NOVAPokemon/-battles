package main

import (
	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/api"
)

const GetBattlesName = "GET_BATTLES"
const StartBattleName = "START_BATTLE"
const JoinBattleName = "JOIN_BATTLE"
const QueueForBattleName = "QUEUE_FOR_BATTLE"

const GET = "GET"
const POST = "POST"

var routes = utils.Routes{
	utils.Route{
		Name:        GetBattlesName,
		Method:      GET,
		Pattern:     api.GetBattlesPath,
		HandlerFunc: GetCurrentLobbies,
	},
	utils.Route{
		Name:        StartBattleName,
		Method:      GET,
		Pattern:     api.StartBattlePath,
		HandlerFunc: CreateBattleLobby,
	},
	utils.Route{
		Name:        JoinBattleName,
		Method:      GET,
		Pattern:     api.JoinBattlePath,
		HandlerFunc: JoinBattleLobby,
	},

	utils.Route{
		Name:        QueueForBattleName,
		Method:      GET,
		Pattern:     api.QueueForBattlePath,
		HandlerFunc: QueueForBattle,
	},
}
