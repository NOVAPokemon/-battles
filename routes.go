package main

import (
	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/api"
)

const GetBattlesName = "GET_BATTLES"
const ChallengeToBattleName = "START_BATTLE"
const AcceptChallengeName = "JOIN_BATTLE"
const QueueForBattleName = "QUEUE_FOR_BATTLE"

const GET = "GET"

var routes = utils.Routes{
	utils.Route{
		Name:        GetBattlesName,
		Method:      GET,
		Pattern:     api.GetBattlesPath,
		HandlerFunc: GetCurrentLobbies,
	},
	utils.Route{
		Name:        ChallengeToBattleName,
		Method:      GET,
		Pattern:     api.ChallengeToBattleRoute,
		HandlerFunc: CreateBattleLobby,
	},
	utils.Route{
		Name:        AcceptChallengeName,
		Method:      GET,
		Pattern:     api.AcceptChallengeRoute,
		HandlerFunc: JoinBattleLobby,
	},

	utils.Route{
		Name:        QueueForBattleName,
		Method:      GET,
		Pattern:     api.QueueForBattlePath,
		HandlerFunc: QueueForBattle,
	},
}
