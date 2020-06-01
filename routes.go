package main

import (
	"fmt"
	"github.com/NOVAPokemon/utils"
	"github.com/NOVAPokemon/utils/api"
	"strings"
)

const (
	GetLobbiesName        = "GET_LOBBIES"
	QueueForBattleName    = "QUEUE_FOR_BATTLE"
	ChallengeToBattleName = "CHALLENGE_FOR_BATTLE"
	AcceptChallengeName   = "ACCEPT_BATTLE"
	RejectChallengeName   = "REJECT_BATTLE"
)

const GET = "GET"
const POST = "POST"

var routes = utils.Routes{
	api.GenStatusRoute(strings.ToLower(fmt.Sprintf("/%s", serviceName))),
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
	utils.Route{
		Name:        RejectChallengeName,
		Method:      POST,
		Pattern:     api.RejectChallengeRoute,
		HandlerFunc: HandleRejectChallenge,
	},
}
