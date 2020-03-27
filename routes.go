package main

import (
	"github.com/NOVAPokemon/utils"
)

const GetBattlesName = "GET_BATTLES"
const StartBattleName = "START_BATTLE"
const JoinBattleName = "JOIN_BATTLE"

const GET = "GET"
const POST = "POST"

const GetBattlesPath = "/battles"
const StartBattlePath = "/battles/join"
const JoinBattlePath = "/battles/join/{battleId}"

var routes = utils.Routes{
	utils.Route{
		Name:        GetBattlesName,
		Method:      GET,
		Pattern:     GetBattlesPath,
		HandlerFunc: GetCurrentLobbies,
	},
	utils.Route{
		Name:        StartBattleName,
		Method:      GET,
		Pattern:     StartBattlePath,
		HandlerFunc: CreateBattleLobby,
	},
	utils.Route{
		Name:        JoinBattleName,
		Method:      GET,
		Pattern:     JoinBattlePath,
		HandlerFunc: JoinBattleLobby,
	},
}
