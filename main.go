package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed index.html
var beautifulChatPage []byte

type TextMessage struct {
	Present bool   `json:"present"`
	Sender  string `json:"sender"`
	Text    string `json:"text"`
}

type PingMessage struct {
	Present bool `json:"present"`
}

type SystemMessage struct {
	Present bool   `json:"present"`
	Text    string `json:"text"`
}

type Message struct {
	ID     string        `json:"id"`
	Time   string        `json:"time"` // RFC3339
	Text   TextMessage   `json:"text"`
	Ping   PingMessage   `json:"ping"`
	System SystemMessage `json:"system"`
}

type chatter struct {
	receivers     sync.Map
	receiverCount int32
	ctr           uint32
}

func (s *chatter) NewID() string {
	ctr := atomic.AddUint32(&s.ctr, 1)
	return fmt.Sprintf("%X.%X", time.Now().Unix(), ctr)
}

func (s *chatter) NewMessage() Message {
	return Message{
		ID:   s.NewID(),
		Time: time.Now().Format(time.RFC3339),
	}
}

func (s *chatter) Send(message Message) {
	s.receivers.Range(func(key any, value any) bool {
		value.(chan Message) <- message
		return true
	})
}

func (s *chatter) Receive() (string, <-chan Message, func()) {
	id := s.NewID()
	ch := make(chan Message, 10)
	close := func() {
		s.receivers.Delete(id)
		close(ch)

		{
			currentCount := atomic.AddInt32(&s.receiverCount, -1)
			msg := s.NewMessage()
			msg.System.Present = true
			msg.System.Text = fmt.Sprintf("(--chatter)==%v", currentCount)
			s.Send(msg)
		}
	}
	s.receivers.Store(id, ch)

	{
		currentCount := atomic.AddInt32(&s.receiverCount, 1)
		msg := s.NewMessage()
		msg.System.Present = true
		msg.System.Text = fmt.Sprintf("(++chatter)==%v", currentCount)
		s.Send(msg)
	}

	return id, ch, close
}

func main() {
	{
		logLevel := slog.LevelInfo
		if os.Getenv("CREAMY_CHAT_DEBUG") != "" {
			logLevel = slog.LevelDebug
		}

		h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})
		slog.SetDefault(slog.New(h))
	}

	c := chatter{}

	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()

		for range t.C {
			msg := c.NewMessage()
			msg.Ping.Present = true
			c.Send(msg)
		}
	}()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write(beautifulChatPage)
		slog.Debug("served landing page")
	})

	http.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		txtMessage := TextMessage{}
		if err := json.NewDecoder(r.Body).Decode(&txtMessage); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Bad Request: Invalid JSON"))
			slog.Debug("error while decoding JSON during /send", "err", err)
			return
		}
		txtMessage.Present = true
		if u, _, ok := r.BasicAuth(); ok {
			txtMessage.Sender = u
		}
		if txtMessage.Sender == "" {
			txtMessage.Sender = "anon"
		}

		msg := c.NewMessage()
		msg.Text = txtMessage

		w.Header().Set("Content-Type", "text/json")
		json.NewEncoder(w).Encode(&msg)

		slog.Debug("sending text message", "msg", msg.ID)
		c.Send(msg)
	})

	http.HandleFunc("/receive", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/jsonl")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Expires", "0")

		id, msgs, close := c.Receive()
		slog.Debug("receiver +", "rcv", id)
		defer func() {
			close()
			slog.Debug("receiver -", "rcv", id)
		}()

		encoder := json.NewEncoder(w)
		write := func(msg Message) error {
			if err := encoder.Encode(msg); err != nil {
				slog.Debug("error encoding message", "rcv", id, "msg", msg.ID, "err", err)
				return err
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				slog.Debug("error writing LF", "rcv", id, "msg", msg.ID, "err", err)
				return err
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			} else {
				slog.Debug("w does not implement http.Flusher, messages may be delayed?")
			}
			slog.Debug("message -> receiver", "rcv", id, "msg", msg.ID)
			return nil
		}

		{
			msg := c.NewMessage()
			msg.ID = "SERVER-HELLO"
			msg.Ping.Present = true
			if err := write(msg); err != nil {
				return
			}
		}

		for {
			select {
			case msg := <-msgs:
				if err := write(msg); err != nil {
					return
				}
			case <-r.Context().Done():
				return
			}
		}
	})

	slog.Info("listening", "addr", ":3000")
	err := http.ListenAndServe(":3000", nil)
	if err != nil {
		slog.Error("failed to listen on :3000", "err", err)
		os.Exit(1)
	}
}
