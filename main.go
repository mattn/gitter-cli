package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/oauth2"

	"github.com/skratchdot/open-golang/open"

	"github.com/sromku/go-gitter"
)

func getConfig() (string, map[string]string, error) {
	dir := os.Getenv("HOME")
	if dir == "" && runtime.GOOS == "windows" {
		dir = filepath.Join(os.Getenv("APPDATA"), "gitter-cli")
	} else {
		dir = filepath.Join(dir, ".config", "gitter-cli")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", nil, err
	}
	file := filepath.Join(dir, "settings.json")
	config := map[string]string{}

	b, err := ioutil.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return "", nil, err
	}
	if err != nil {
		config["ClientID"] = "5e32e7aee048a37152d3d3b2747e304bd0d963c5"
		config["ClientSecret"] = "387ed62d6df58586cdeaf6313c094343413149fd"
	} else {
		err = json.Unmarshal(b, &config)
		if err != nil {
			return "", nil, fmt.Errorf("could not unmarshal %v: %v", file, err)
		}
	}
	return file, config, nil
}

func getAccessToken(config map[string]string) (string, error) {
	l, err := net.Listen("tcp", "localhost:9998")
	if err != nil {
		return "", err
	}
	defer l.Close()

	oauthConfig := &oauth2.Config{
		Scopes: []string{
			"flow",
		},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://gitter.im/login/oauth/authorize",
			TokenURL: "https://gitter.im/login/oauth/token",
		},
		ClientID:     config["ClientID"],
		ClientSecret: config["ClientSecret"],
		RedirectURL:  "http://localhost:9998/",
	}

	stateBytes := make([]byte, 16)
	_, err = rand.Read(stateBytes)
	if err != nil {
		return "", err
	}

	state := fmt.Sprintf("%x", stateBytes)
	err = open.Start(oauthConfig.AuthCodeURL(state, oauth2.SetAuthURLParam("response_type", "code")))
	if err != nil {
		return "", err
	}

	quit := make(chan string)
	go http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`<script>window.open("about:blank","_self").close()</script>`))
		w.(http.Flusher).Flush()
		quit <- req.URL.Query().Get("code")
	}))

	code := <-quit

	token, err := oauthConfig.Exchange(context.Background(), code)
	if err != nil {
		return "", err
	}
	return token.AccessToken, nil
}

func main() {
	var room string
	var debug bool
	flag.BoolVar(&debug, "debug", false, "debug mode")
	flag.StringVar(&room, "room", "", "URI of room: community/room")
	flag.Parse()
	if room == "" {
		flag.Usage()
		os.Exit(2)
	}

	file, config, err := getConfig()
	if err != nil {
		log.Fatal("failed to get configuration:", err)
	}
	if config["AccessToken"] == "" {
		token, err := getAccessToken(config)
		if err != nil {
			log.Fatal("faild to get access token:", err)
		}
		config["AccessToken"] = token
		b, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			log.Fatal("failed to store file:", err)
		}
		err = ioutil.WriteFile(file, b, 0700)
		if err != nil {
			log.Fatal("failed to store file:", err)
		}
	}

	api := gitter.New(config["AccessToken"])
	api.SetDebug(debug, os.Stderr)
	roomId, err := api.GetRoomId(room)
	if err != nil {
		log.Fatal("failed to get room:", err)
	}
	faye := api.Faye(roomId)
	go faye.Listen()

	for {
		event := <-faye.Event
		switch ev := event.Data.(type) {
		case *gitter.MessageReceived:
			fmt.Printf("%s (%s): %s\n",
				ev.Message.Sent.Format("2006/01/02 15:04:05"),
				ev.Message.From.Username,
				ev.Message.Text)
		case *gitter.GitterConnectionClosed:
			log.Fatal(ev)
		}
	}
}
