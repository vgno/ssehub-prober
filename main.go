package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	uuid "github.com/satori/go.uuid"
	"github.com/spf13/viper"
	"github.com/streadway/amqp"
	statsd "gopkg.in/alexcesaro/statsd.v2"

	"github.com/vgno/ssehub-prober/sse"
)

func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
	}
}

var probesSent = make(map[string]time.Time)
var probesReceived = make(chan *sse.Event)
var statsChannel = make(chan float64)

func amqpHandler() {
	host := fmt.Sprintf("amqp://%s:%d/", viper.GetString("amqp.host"), viper.GetInt("amqp.port"))
	log.Printf("AMQP: Connecting to %s", host)
	conn, err := amqp.Dial(host)

	failOnError(err, "AMQP: Failed to connect to RabbitMQ")
	defer conn.Close()

	log.Print("AMQP: Connected to amqp")

	ch, err := conn.Channel()
	failOnError(err, "AMQP: Failed to open a channel")
	defer ch.Close()

	for {
		id, _ := uuid.NewV4()
		body := fmt.Sprintf("{\"event\": \"probe\", \"id\": \"%s\", \"path\": \"%s\", \"data\": \"{}\"}", id.String(), viper.GetString("amqp.path"))
		err = ch.Publish(
			viper.GetString("amqp.exchange"),
			"#",
			false,
			false,
			amqp.Publishing{
				ContentType: "application/json",
				Body:        []byte(body),
			})

		failOnError(err, "AMQP: Failed to publish a message")
		log.Printf("AMQP: Sent %s", body)

		probesSent[id.String()] = time.Now()

		time.Sleep(time.Duration(viper.GetInt("ssehub.interval")) * time.Second)
	}
}

func probeHandler() {
	sse.Notify(viper.GetString("ssehub.url"), probesReceived, true)

	for {
		v := <-probesReceived
		if v.Type == "probe" {
			if probe, ok := probesSent[v.Id]; ok {
				duration := time.Since(probe)
				diff := float64(duration.Seconds() * 1e3)
				delete(probesSent, v.Id)

				statsChannel <- diff
			}
		}
	}
}

func statsHandler() {
	shouldTrackPrometheus := viper.GetBool("collectors.prometheus.enabled")
	shouldTrackStatsd := viper.GetBool("collectors.statsd.enabled")

	var statsdClient *statsd.Client
	var statsdError error

	if shouldTrackStatsd {
		statsdClient, statsdError = statsd.New(
			statsd.Address(viper.GetString("collectors.statsd.address")),
		)

		failOnError(statsdError, "StatsD: Failed to connect to statsd")
	}

	for {
		v := <-statsChannel

		log.Printf("Oberserving value %f", v)

		if shouldTrackPrometheus {
			prometheusStats.Observe(v)
		}

		if shouldTrackStatsd {
			statsdClient.Histogram("ssehub.response_time", v)
		}
	}
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "OK")
}

var (
	prometheusStats = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "ssehub_response_time_miliseconds",
		Help: "Response time of the ssehub.",
	})
)

func init() {
	// Register the summary and the histogram with Prometheus's default registry.
	prometheus.MustRegister(prometheusStats)
}

func main() {
	viper.AddConfigPath(".")
	viper.SetConfigName("config")
	err := viper.ReadInConfig() // Find and read the config file
	failOnError(err, "Could not read config file")

	viper.WatchConfig()
	viper.OnConfigChange(func(e fsnotify.Event) {
		log.Printf("CONFIG: file changed:", e.Name)
	})

	go probeHandler()
	go amqpHandler()
	go statsHandler()

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/healthz", healthzHandler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
