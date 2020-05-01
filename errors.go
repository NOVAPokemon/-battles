package main

import (
	"fmt"
	"github.com/NOVAPokemon/utils"
	"github.com/pkg/errors"
)

const (
	errorFinishBattleFormat  = "error finishing battle %s"
	errorCommitResultsFormat = "error commmiting results for battle %s"
)

var (
	errorTooManyPokemons   = errors.New("too many pokemons")
	errorNotEnoughPokemons = errors.New("not enough pokemons")
	errorPokemonTokens     = errors.New("invalid pokemon hashes")
	errorStatsToken     = errors.New("invalid stats token")
	errorItemsToken     = errors.New("invalid items token")
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

// Other Functions
func wrapFinishBattleError(err error, battleId string) error {
	return errors.Wrap(err, fmt.Sprintf(errorFinishBattleFormat, battleId))
}

func wrapCommitResultsError(err error, battleId string) error {
	return errors.Wrap(err, fmt.Sprintf(errorCommitResultsFormat, battleId))
}
