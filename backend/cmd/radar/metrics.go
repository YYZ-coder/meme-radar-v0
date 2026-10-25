package main

// metricsAddr is where every role serves /metrics (Prometheus scrapes
// <service>:9100 on the compose network); RADAR_METRICS_ADDR=off disables it.
func metricsAddr(getenv func(string) string) string {
	if a := getenv("RADAR_METRICS_ADDR"); a != "" {
		return a
	}
	return ":9100"
}
