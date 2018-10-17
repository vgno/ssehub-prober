// Copied from https://github.com/andrewstuart/go-sse

package sse

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

//SSE name constants
const (
	eName = "event"
	dName = "data"
	iName = "id"
)

var (
	//ErrNilChan will be returned by Notify if it is passed a nil channel
	ErrNilChan = fmt.Errorf("nil channel given")
)

//Client is the default client used for requests.
var Client = &http.Client{
	Timeout: time.Minute * 5,
}

func liveReq(method, uri string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, uri, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "text/event-stream")

	return req, nil
}

//Event is a go representation of an http server-sent event
type Event struct {
	URI  string
	Type string
	Id   string
	Data io.Reader
}

//Notify takes a uri and channel, and will send an Event down the channel when
//recieved.
func Notify(uri string, evCh chan *Event, reconnect bool) error {
	if evCh == nil {
		return ErrNilChan
	}

	req, err := liveReq("GET", uri, nil)
	if err != nil {
		return fmt.Errorf("error getting sse request: %v", err)
	}

	go func() {
		for {
			log.Printf("SSE: Connecting to %s", uri)
			res, err := Client.Do(req)
			if err != nil {
				fmt.Errorf("error performing request for %s: %v", uri, err)
				break
			}

			br := bufio.NewReader(res.Body)
			defer res.Body.Close()

			delim := []byte{':', ' '}

			var currEvent *Event = &Event{URI: uri}

			for {
				bs, err := br.ReadBytes('\n')

				if err != nil {
					fmt.Errorf("Error reading bytes: %v.", err)
					break
				}

				if string(bs) == "\n" {
					evCh <- currEvent
					currEvent = &Event{URI: uri}
					continue
				}

				if len(bs) < 2 {
					continue
				}

				spl := bytes.Split(bs, delim)

				if len(spl) < 2 {
					continue
				}

				switch string(spl[0]) {
				case eName:
					currEvent.Type = string(bytes.TrimSpace(spl[1]))
				case iName:
					currEvent.Id = string(bytes.TrimSpace(spl[1]))
				case dName:
					currEvent.Data = bytes.NewBuffer(bytes.TrimSpace(spl[1]))
				}
			}
		}
	}()

	return nil
}
