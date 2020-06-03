package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	startedBattles = promauto.NewCounter(prometheus.CounterOpts{
		Name: "battles_started",
		Help: "The total number of battles started",
	})
	finishedBattles = promauto.NewCounter(prometheus.CounterOpts{
		Name: "battles_finished",
		Help: "The total number of battles finished",
	})
	ongoingBattles = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "battles_ongoing_battles",
		Help: "The total number of ongoing battles",
	})
)

func emitStartBattle() {
	startedBattles.Inc()
}

func emitEndBattle() {
	finishedBattles.Inc()
}

func emitNrOngoingBattles() {
	length := 0
	hub.ongoingBattles.Range(func(_, _ interface{}) bool {
		length++
		return true
	})
	ongoingBattles.Set(float64(length))
}

// metrics for prometheus
func recordMetrics() {
	go func() {
		for {
			emitNrOngoingBattles()
			time.Sleep(2 * time.Second)
		}
	}()
}
