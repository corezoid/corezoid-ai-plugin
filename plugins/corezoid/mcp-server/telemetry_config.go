package main

import (
	"os"
	"strconv"
)

const (
	defaultAnalyticsEndpoint = "https://www.corezoid.com/api/2/json/public/1852976/5b76d006818d63730bc18a5b0e7d8d091e82d2a2"
	defaultAnalyticsConvID   = 1852976

	defaultFeedbackEndpoint = "https://www.corezoid.com/api/2/json/public/1871779/8232d5d191a194eff64169d6d5e0ebc6217efecc"
	defaultFeedbackConvID   = 1871779
)

type telemetryConfig struct {
	AnalyticsEndpoint string
	AnalyticsConvID   int
	FeedbackEndpoint  string
	FeedbackConvID    int
}

// loadTelemetryConfig reads telemetry configuration from environment variables,
// falling back to built-in defaults. Called once at startup by initAnalytics.
func loadTelemetryConfig() telemetryConfig {
	return telemetryConfig{
		AnalyticsEndpoint: envOr("COREZOID_ANALYTICS_ENDPOINT", defaultAnalyticsEndpoint),
		AnalyticsConvID:   envOrInt("COREZOID_ANALYTICS_CONV_ID", defaultAnalyticsConvID),
		FeedbackEndpoint:  envOr("COREZOID_FEEDBACK_ENDPOINT", defaultFeedbackEndpoint),
		FeedbackConvID:    envOrInt("COREZOID_FEEDBACK_CONV_ID", defaultFeedbackConvID),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
