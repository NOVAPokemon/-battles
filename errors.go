package main

import (
	"fmt"
	"github.com/NOVAPokemon/utils"
	"github.com/pkg/errors"
)

const (
	errorMainLoop  = "error in battle main loop"
	errorSetupLoop = "error in battle setup loop"

	errorCommitResultsFormat = "error commmiting results for battle %s"
	errorStartBattleFormat   = "error starting battle %s"

	errorUserFormat = "error occurred with user %s"
)

var (
	errorTooManyPokemons    = errors.New("too many pokemons")
	errorNotEnoughPokemons  = errors.New("not enough pokemons")
	errorBattleDoesNotExist = errors.New("battle does not exist")
	errorPlayerNotOnline    = errors.New("challenged player not online")
	errorPlayerUnauthorized = errors.New("player is not authorized to join this battle")
)

// Handlers
func wrapGetLobbiesError(err error) error {
	return errors.Wrap(err, fmt.Sprintf(utils.ErrorInHandlerFormat, GetLobbiesName))
}

func wrapQueueBattleError(err error) error {
	return errors.Wrap(err, fmt.Sprintf(utils.ErrorInHandlerFormat, QueueForBattleName))
}

func wrapChallengeToBattleError(err error) error {
	return errors.Wrap(err, fmt.Sprintf(utils.ErrorInHandlerFormat, ChallengeToBattleName))
}

func wrapAcceptChallengeError(err error) error {
	return errors.Wrap(err, fmt.Sprintf(utils.ErrorInHandlerFormat, AcceptChallengeName))
}

func wrapRejectChallengeError(err error) error {
	return errors.Wrap(err, fmt.Sprintf(utils.ErrorInHandlerFormat, RejectChallengeName))
}

// Other Functions

func wrapCommitResultsError(err error, battleId string) error {
	return errors.Wrap(err, fmt.Sprintf(errorCommitResultsFormat, battleId))
}

func wrapStartBattleError(err error, battleId string) error {
	return errors.Wrap(err, fmt.Sprintf(errorStartBattleFormat, battleId))
}

func wrapMainLoopError(err error) error {
	return errors.Wrap(err, errorMainLoop)
}

func wrapSetupLoopError(err error) error {
	return errors.Wrap(err, errorSetupLoop)
}

// Errors

func newUserError(username string) error {
	return errors.New(fmt.Sprintf(errorUserFormat, username))
}
