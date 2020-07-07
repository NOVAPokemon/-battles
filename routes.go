package main

import (
	"strings"

	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/api"
)

const (
	getLobbiesName        = "GET_LOBBIES"
	queueForBattleName    = "QUEUE_FOR_BATTLE"
	challengeToBattleName = "CHALLENGE_FOR_BATTLE"
	acceptChallengeName   = "ACCEPT_BATTLE"
	rejectChallengeName   = "REJECT_BATTLE"
)

const (
	get  = "GET"
	post = "POST"
)

var routes = utils.Routes{
	api.GenStatusRoute(strings.ToLower(serviceName)),
	utils.Route{
		Name:        getLobbiesName,
		Method:      get,
		Pattern:     api.GetBattlesPath,
		HandlerFunc: handleGetCurrentLobbies,
	},
	utils.Route{
		Name:        challengeToBattleName,
		Method:      get,
		Pattern:     api.ChallengeToBattleRoute,
		HandlerFunc: handleChallengeToBattle,
	},
	utils.Route{
		Name:        acceptChallengeName,
		Method:      get,
		Pattern:     api.AcceptChallengeRoute,
		HandlerFunc: handleAcceptChallenge,
	},
	utils.Route{
		Name:        queueForBattleName,
		Method:      get,
		Pattern:     api.QueueForBattlePath,
		HandlerFunc: handleQueueForBattle,
	},
	utils.Route{
		Name:        rejectChallengeName,
		Method:      post,
		Pattern:     api.RejectChallengeRoute,
		HandlerFunc: handleRejectChallenge,
	},
}
