FROM golang:1.21 as builder

ENV CGO_ENABLED=0 \
  GOOS=linux \
  GOARCH=amd64

WORKDIR $GOPATH/src/github.com/AlbinoDrought/creamy-chat/

COPY go.mod $GOPATH/src/github.com/AlbinoDrought/creamy-chat/
RUN go mod download

COPY main.go index.html openpgp.min.js $GOPATH/src/github.com/AlbinoDrought/creamy-chat/
RUN go build -a -installsuffix cgo -o /go/bin/creamy-chat

FROM scratch
COPY --from=builder /go/bin/creamy-chat /go/bin/creamy-chat
ENTRYPOINT ["/go/bin/creamy-chat"]
