package main

type BattleServerConfig struct {
	DefaultCooldown   int `json:"default_cooldown"` // milliseconds
	PokemonsPerBattle int `json:"pokemons_per_battle"`
	BattleStartTimeout int `json:"battle_start_timeout"` // seconds
}
