package main

import (
	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/api"
	"strings"
)

const (
	GetLobbiesName        = "GET_LOBBIES"
	QueueForBattleName    = "QUEUE_FOR_BATTLE"
	ChallengeToBattleName = "CHALLENGE_FOR_BATTLE"
	AcceptChallengeName   = "ACCEPT_BATTLE"
)

const GET = "GET"

var routes = utils.Routes{
	api.GenStatusRoute(strings.ToLower(serviceName)),
	utils.Route{
		Name:        GetLobbiesName,
		Method:      GET,
		Pattern:     api.GetBattlesPath,
		HandlerFunc: HandleGetCurrentLobbies,
	},
	utils.Route{
		Name:        ChallengeToBattleName,
		Method:      GET,
		Pattern:     api.ChallengeToBattleRoute,
		HandlerFunc: HandleChallengeToBattle,
	},
	utils.Route{
		Name:        AcceptChallengeName,
		Method:      GET,
		Pattern:     api.AcceptChallengeRoute,
		HandlerFunc: HandleAcceptChallenge,
	},

	utils.Route{
		Name:        QueueForBattleName,
		Method:      GET,
		Pattern:     api.QueueForBattlePath,
		HandlerFunc: HandleQueueForBattle,
	},
}
