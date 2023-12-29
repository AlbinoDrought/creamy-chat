# Creamy Chat

<p align="center">
    <img src="./logo.png" alt="Creamy Chat Cat Logo">
    <p align="center">Simple JSONL Chat Server</p>
    <p align="center">
        <a href="https://github.com/AlbinoDrought/creamy-chat/blob/master/LICENSE"><img alt="AGPL-3.0 License" src="https://img.shields.io/github/license/AlbinoDrought/creamy-chat"></a>
    </p>
</p>

## Screenshots

![](./screenshot.png)

## Usage

Run:

```
docker run \
  --rm \
  -p 3000:3000 \
  ghcr.io/albinodrought/creamy-chat:latest
```

Visit http://localhost:3000/

Usernames will be loaded from basic auth if provided.

For more debugging output, run with `CREAMY_CHAT_DEBUG=1`

## Building

### With Docker

`docker build -t ghcr.io/albinodrought/creamy-chat:latest .`

### Without Docker

`go get && go build`
