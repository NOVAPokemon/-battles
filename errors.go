package main

import (
	"fmt"

	"github.com/NOVAPokemon/utils"
	"github.com/pkg/errors"
)

const (
	errorMainLoop  = "error in battle main loop"

	errorCommitResultsFormat = "error commmiting results for battle %s"
	errorStartBattleFormat   = "error starting battle %s"

	errorUserFormat = "error occurred with user %s"

	errorAddPlayer = "error adding player to battle"
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
	return errors.Wrap(err, fmt.Sprintf(utils.ErrorInHandlerFormat, getLobbiesName))
}

func wrapQueueBattleError(err error) error {
	return errors.Wrap(err, fmt.Sprintf(utils.ErrorInHandlerFormat, queueForBattleName))
}

func wrapChallengeToBattleError(err error) error {
	return errors.Wrap(err, fmt.Sprintf(utils.ErrorInHandlerFormat, challengeToBattleName))
}

func wrapAcceptChallengeError(err error) error {
	return errors.Wrap(err, fmt.Sprintf(utils.ErrorInHandlerFormat, acceptChallengeName))
}

func wrapRejectChallengeError(err error) error {
	return errors.Wrap(err, fmt.Sprintf(utils.ErrorInHandlerFormat, rejectChallengeName))
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

func wrapAddPlayerError(err error) error {
	return errors.Wrap(err, errorAddPlayer)
}

// Errors

func newUserError(username string) error {
	return errors.New(fmt.Sprintf(errorUserFormat, username))
}
