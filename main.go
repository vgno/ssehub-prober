package main

import (
    "log"
    "fmt"
    "time"
    //"net/http"

    "github.com/satori/go.uuid"
    "github.com/spf13/viper"
    "github.com/streadway/amqp"
    "github.com/fsnotify/fsnotify"
    // "github.com/DataDog/datadog-go/statsd"
    "github.com/prometheus/client_golang/prometheus"
    //"github.com/prometheus/client_golang/prometheus/promhttp"

    "github.com/vgno/ssehub-prober/sse"
)

func failOnError(err error, msg string) {
    if err != nil {
        log.Fatalf("%s: %s", msg, err)
    }
}

var probesSent = make(map[string]time.Time)
var probesReceived = make(chan *sse.Event)
var statsChannel = make(chan int)

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
        id := uuid.NewV4().String()
        body := fmt.Sprintf( "{\"event\": \"probe\", \"id\": \"%s\", \"path\": \"%s\", \"data\": \"{}\"}", id, viper.GetString("amqp.path"))
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

        probesSent[id] = time.Now()

        time.Sleep(time.Duration(viper.GetInt("ssehub.interval")) * time.Second)
    }
}

func probeHandler() {
    sse.Notify(viper.GetString("ssehub.url"), probesReceived)

    log.Printf("SSE: Connected to %s", viper.GetString("ssehub.url"))

    for {
        v := <- probesReceived
        if (v.Type == "probe") {
            if probe, ok := probesSent[v.Id]; ok {
                duration := time.Since(probe)
                diff := int(duration.Nanoseconds() / 1000 / 1000)
                delete(probesSent, v.Id)

                statsChannel <- diff
            }
        }
    }
}

func statsHandler() {
    shouldTrackPrometheus := viper.GetBool("collectors.prometheus.enabled")

    for {
        v := <- statsChannel

        if (shouldTrackPrometheus) {
            log.Printf("Oberserving value in prometheus %d", v)
            prometheusStats.Observe(float64(v))
        }
    }
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
    for {}
}