package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed index.html
var beautifulChatPage []byte

//go:embed openpgp.min.js
var openpgpJS []byte

type TextMessage struct {
	Present bool   `json:"present"`
	Sender  string `json:"sender"`
	Text    string `json:"text"`
}

type FileMessage struct {
	Present    bool   `json:"present"`
	Sender     string `json:"sender"`
	ClientUUID string `json:"client_uuid"`
	Filename   string `json:"filename"`
	Mimetype   string `json:"mimetype"`
	HashSHA256 string `json:"hash_sha256"`
	DataB64    string `json:"data_b64"`
	TotalSize  uint64 `json:"total_size"`
	Offset     uint64 `json:"offset"`
}

type PingMessage struct {
	Present bool   `json:"present"`
	Random  string `json:"random"`
}

type SystemMessage struct {
	Present bool   `json:"present"`
	Text    string `json:"text"`
}

type Message struct {
	ID     string        `json:"id"`
	Time   string        `json:"time"` // RFC3339
	Text   TextMessage   `json:"text"`
	File   FileMessage   `json:"file"`
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
			msg.System.Text = fmt.Sprintf("total chatters -1 = %v", currentCount)
			s.Send(msg)
		}
	}
	s.receivers.Store(id, ch)

	{
		currentCount := atomic.AddInt32(&s.receiverCount, 1)
		msg := s.NewMessage()
		msg.System.Present = true
		msg.System.Text = fmt.Sprintf("total chatters +1 = %v", currentCount)
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

	chunkSizeLimit := 2 * 1024 * 1024     // 2MB (max encoded chunk size)
	sizeLimit := uint64(20 * 1024 * 1024) // 20MB
	sizeLimitEnv := os.Getenv("CREAMY_CHAT_FILE_SIZE_LIMIT")
	if sizeLimitEnv != "" {
		sizeLimitEnvI, err := strconv.ParseUint(sizeLimitEnv, 10, 64)
		if err != nil {
			slog.Error("failed to parse CREAMY_CHAT_FILE_SIZE_LIMIT", "env", sizeLimitEnv, "err", err)
			os.Exit(1)
		}
		sizeLimit = sizeLimitEnvI
	}

	c := chatter{}

	go func() {
		t := time.NewTimer(10 * time.Second)
		defer t.Stop()

		for range t.C {
			msg := c.NewMessage()
			msg.Ping.Present = true
			length := []byte{0}
			if _, err := rand.Read(length); err != nil {
				slog.Error("failed to gen random length, using placeholder", "err", err)
				length[0] = 69
			}
			if length[0] > 100 {
				slog.Debug("capped ping junk length to 100", "orig", length[0])
				length[0] = 100
			}
			randBytes := make([]byte, length[0])
			if _, err := rand.Read(randBytes); err != nil {
				slog.Error("failed to gen random bytes, using idx", "err", err)
				for i := range randBytes {
					randBytes[i] = byte(i)
				}
			}
			msg.Ping.Random = base64.StdEncoding.EncodeToString(randBytes)

			c.Send(msg)

			// send another ping anywhere from 2s to 30s from now
			nextPing := 2 + (length[0] % (30 - 2))
			t.Reset(time.Duration(nextPing) * time.Second)
		}
	}()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write(beautifulChatPage)
		slog.Debug("served landing page")
	})

	http.HandleFunc("/openpgp.min.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Write(openpgpJS)
		slog.Debug("served openpgp js")
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

		w.Header().Set("Creamy-Chat-Message-ID", msg.ID)
		w.WriteHeader(http.StatusNoContent)

		slog.Debug("sending text message", "msg", msg.ID)
		c.Send(msg)
	})

	http.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) {
		fileMessage := FileMessage{}
		if err := json.NewDecoder(r.Body).Decode(&fileMessage); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Bad Request: Invalid JSON"))
			slog.Debug("error while decoding JSON during /file", "err", err)
			return
		}
		fileMessage.Present = true
		if u, _, ok := r.BasicAuth(); ok {
			fileMessage.Sender = u
		}
		if fileMessage.Sender == "" {
			fileMessage.Sender = "anon"
		}
		if fileMessage.TotalSize == 0 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Bad Request: File must have size"))
			return
		}
		if sizeLimit != 0 {
			if fileMessage.TotalSize > sizeLimit {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte("Bad Request: File Too Large"))
				return
			}
			if fileMessage.Offset > sizeLimit {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte("Bad Request: Offset Too Large"))
				return
			}
		}
		if len(fileMessage.DataB64) > chunkSizeLimit {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Bad Request: Chunk Too Large"))
			return
		}

		msg := c.NewMessage()
		msg.File = fileMessage

		w.Header().Set("Creamy-Chat-Message-ID", msg.ID)
		w.WriteHeader(http.StatusNoContent)

		slog.Debug("sending file message", "msg", msg.ID)
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
