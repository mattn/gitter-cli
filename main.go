package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/oauth2"

	"github.com/skratchdot/open-golang/open"

	"github.com/sromku/go-gitter"
	"github.com/urfave/cli"
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

func stream(c *cli.Context) error {
	room := c.String("room")
	if room == "" {
		cli.ShowCommandHelp(c, "stream")
		return nil
	}
	ojson := c.Bool("json")

	config := c.App.Metadata["config"].(map[string]string)
	api := gitter.New(config["AccessToken"])
	api.SetDebug(c.GlobalBool("debug"), os.Stderr)

	roomId, err := api.GetRoomId(room)
	if err != nil {
		return fmt.Errorf("failed to get room: %v", err)
	}
	faye := api.Faye(roomId)
	go faye.Listen()

	for {
		event := <-faye.Event
		switch ev := event.Data.(type) {
		case *gitter.MessageReceived:
			if ojson {
				json.NewEncoder(os.Stdout).Encode(ev)
			} else {
				fmt.Printf("%s (%s): %s\n",
					ev.Message.Sent.Format("2006/01/02 15:04:05"),
					ev.Message.From.Username,
					ev.Message.Text)
			}
		case *gitter.GitterConnectionClosed:
			return fmt.Errorf("connection closed: %v", err)
		}
	}
}

func recent(c *cli.Context) error {
	room := c.String("room")
	if room == "" {
		cli.ShowCommandHelp(c, "recent")
		return nil
	}
	ojson := c.Bool("json")

	config := c.App.Metadata["config"].(map[string]string)
	api := gitter.New(config["AccessToken"])
	api.SetDebug(c.GlobalBool("debug"), os.Stderr)

	roomId, err := api.GetRoomId(room)
	if err != nil {
		return err
	}
	messages, err := api.GetMessages(roomId, nil)
	if err != nil {
		return fmt.Errorf("failed to get messges: %v", err)
	}
	if ojson {
		json.NewEncoder(os.Stdout).Encode(messages)
	} else {
		for _, message := range messages {
			fmt.Printf("%s (%s): %s\n",
				message.Sent.Format("2006/01/02 15:04:05"),
				message.From.Username,
				message.Text)
		}
	}
	return nil
}

func update(c *cli.Context) error {
	room := c.String("room")
	if room == "" {
		cli.ShowCommandHelp(c, "stream")
		return nil
	}
	var status string
	if c.Bool("stdin") {
		b, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		status = string(b)
	} else {
		status = strings.Join(c.Args(), " ")
	}
	if status == "" {
		cli.ShowCommandHelp(c, "update")
		return nil
	}

	config := c.App.Metadata["config"].(map[string]string)
	api := gitter.New(config["AccessToken"])
	api.SetDebug(c.GlobalBool("debug"), os.Stderr)

	roomId, err := api.GetRoomId(room)
	if err != nil {
		return err
	}
	_, err = api.SendMessage(roomId, status)
	return err
}

func initialize(c *cli.Context) error {
	file, config, err := getConfig()
	if err != nil {
		return fmt.Errorf("failed to get configuration: %v", err)
	}
	if config["AccessToken"] == "" {
		token, err := getAccessToken(config)
		if err != nil {
			return fmt.Errorf("faild to get access token: %v", err)
		}
		config["AccessToken"] = token
		b, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to store file: %v", err)
		}
		err = ioutil.WriteFile(file, b, 0700)
		if err != nil {
			return fmt.Errorf("failed to store file: %v", err)
		}
	}
	c.App.Metadata["config"] = config
	return nil
}

func main() {
	app := cli.NewApp()
	app.Name = "gitter-cli"
	app.Usage = "client app for gitter.com"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "Debug mode",
		},
	}
	app.Commands = []cli.Command{
		{
			Name:    "recent",
			Aliases: []string{"r"},
			Usage:   "Show recent messages",
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:  "json",
					Usage: "Output JSON",
				},
				cli.StringFlag{
					Name:  "room",
					Usage: "Room URI (ex: community/room)",
				},
			},
			Action: recent,
		},
		{
			Name:    "stream",
			Aliases: []string{"s"},
			Usage:   "Watch the room",
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:  "json",
					Usage: "Output JSON",
				},
				cli.StringFlag{
					Name:  "room",
					Usage: "Room URI (ex: community/room)",
				},
			},
			Action: stream,
		},
		{
			Name:    "update",
			Aliases: []string{"u"},
			Usage:   "Update",
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:  "stdin",
					Usage: "Read from stdin",
				},
				cli.StringFlag{
					Name:  "room",
					Usage: "Room URI (ex: community/room)",
				},
			},
			Action: update,
		},
	}
	app.Version = "0.0.1"
	app.Author = "mattn"
	app.Email = "mattn.jp@gmail.com"
	app.EnableBashCompletion = true
	app.Before = initialize
	app.Setup()
	app.Run(os.Args)
}
