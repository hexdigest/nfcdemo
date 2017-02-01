package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/fuzxxl/nfc/2.0/nfc"
	"github.com/gorilla/websocket"
	"github.com/hexdigest/nfcdemo/emv"
	"github.com/hexdigest/nfcdemo/listener"
)

type Config struct {
	Listen          string
	BaseURL         string
	DelayAfterError int //ms
}

func main() {
	f, err := os.Open("config.json")
	if err != nil {
		log.Fatalf("failed to open configuration file: %v\n", err)
	}

	defer f.Close()

	var cfg Config

	decoder := json.NewDecoder(f)
	if err := decoder.Decode(&cfg); err != nil {
		log.Fatalf("failed to read configuration file: %v\n", err)
	}

	readers, err := nfc.ListDevices()
	if err != nil {
		log.Fatalf("failed to list available readers: %v\n", err)
	}

	if len(readers) == 0 {
		log.Fatalf("no NFC readers found\n")
	}

	lConf := listener.Config{
		ConnString:      readers[0],
		DelayAfterError: time.Millisecond * time.Duration(cfg.DelayAfterError),
		TerminalConfig: emv.TerminalConfig{
			TerminalTransactionQualifiers: 0xb620c000,
			TransactionCurrencyCode:       933, //BYN
			TerminalCountryCode:           112, //BY
		},
		Logger: log.New(newAsyncWriter(os.Stdout, 1000), "", log.LstdFlags),
	}

	cardCh, err := listener.Chan(lConf)
	if err != nil {
		log.Fatalf("failed to create listener for %s: %v\n", readers[0], err)
	}

	http.Handle("/pics/", http.StripPrefix("/pics/", http.FileServer(http.Dir("pics"))))
	http.HandleFunc("/ws", newWebsocketHandler(cardCh))
	http.HandleFunc("/", newRootHandler(cfg.BaseURL))

	http.ListenAndServe(cfg.Listen, nil)
}

type asyncWriter struct {
	w  io.Writer
	ch chan string
}

func newAsyncWriter(w io.Writer, bufSize int) asyncWriter {
	aw := asyncWriter{
		w:  w,
		ch: make(chan string, bufSize),
	}

	go func() {
		for {
			aw.w.Write([]byte(<-aw.ch))
		}
	}()

	return aw
}

// Write implements io.Writer
func (aw asyncWriter) Write(p []byte) (n int, err error) {
	aw.ch <- string(p)
	return len(p), nil
}

func newWebsocketHandler(cardCh <-chan emv.Card) http.HandlerFunc {
	var (
		upgrader websocket.Upgrader // use default options
		sockets  []*websocket.Conn

		chAddSocket = make(chan *websocket.Conn, 10)
		chDelSocket = make(chan *websocket.Conn, 10)
	)

	go func() {
		for {
			select {
			case s := <-chAddSocket:
				log.Printf("adding socket to the pool: %s\n", s.RemoteAddr())
				sockets = append(sockets, s)
			case s := <-chDelSocket:
				log.Printf("removing socket from the pool: %s\n", s.RemoteAddr())
				for i := range sockets {
					if sockets[i] == s {
						sockets = append(sockets[:i], sockets[i+1:]...)
						go s.Close()
						break
					}
				}

			case card := <-cardCh:
				message := map[string]string{
					"uid": card.PAN,
				}

				for _, s := range sockets {
					go func(ws *websocket.Conn) {
						if err := sendMessage(ws, message); err != nil {
							chDelSocket <- ws
						}
					}(s)
				}
			}
		}
	}()

	//http handlers that upgrades http connection to websocket connections
	//and registers connection in the pool
	return func(w http.ResponseWriter, r *http.Request) {
		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("failed to create websocket connection: %v\n", err)
			return
		}

		chAddSocket <- socket
	}
}

func sendMessage(ws *websocket.Conn, message interface{}) error {
	if err := ws.WriteJSON(message); err != nil {
		return fmt.Errorf("failed to write message to socket %+v: %v", ws, err)
	}

	return nil
}

func newRootHandler(baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rootTemplate.Execute(w, baseURL)
	}
}

var rootTemplate = template.Must(template.New("").Parse(`
<!DOCTYPE html>
<html lang="en">
	<head>
		<style>
			body {
					margin: 0;
			}
			iframe {
					display: block;
					border: none;
					height: calc(100vh);
					width: 100%;
			}
		</style>
		<script src="https://ajax.googleapis.com/ajax/libs/jquery/3.1.0/jquery.min.js"></script>
	</head>
  <body>
		<iframe id="iframe" src=""></iframe>
  </body>

  <script type="text/javascript">
    var ws = null

    $(document).ready(function(){
			ws = new WebSocket('ws://' + window.location.host + '/ws')
      ws.onmessage = function(m) {
        s = JSON.parse(m.data)
				$("#iframe").attr("src",  "{{.}}pics/" + s.uid + ".jpg")
      };
    })
  </script>
</html>`))
