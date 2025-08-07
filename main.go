package main

import (
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func init() {
	loadtmpl, err := template.ParseFS(viewsfiles, "views/*.html")
	if err != nil {
		slog.Error("unable to parse html templates", err.Error(), nil)
	}
	tmpl = loadtmpl
}

//go:embed views/*
var viewsfiles embed.FS

const charset = "0123456789abcdefghjkmnopqrstuvwz"

func randomString(length int) string {
	rand.Seed(time.Now().UnixNano())
	result := make([]byte, length)
	for i := range result {
		result[i] = charset[rand.Intn(len(charset))]
	}
	return string(result)
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type board [9]int
type Player struct {
	ID   string
	conn *websocket.Conn
}

type Game struct {
	PP1      Player
	PP2      Player
	NextMove int
	State    board
}

type GameStateResponse struct {
	Type     string `json:"type"`
	State    board  `json:"state"`
	Result   int    `json:"result"`
	NextMove int    `json:"nextMove"`
}
type InfoResonse struct {
	Type string `json:"type"`
	Msg  string `json:"msg"`
}

var tmpl *template.Template

var GameBoards = make(map[string]*Game, 0)

func WShandler(w http.ResponseWriter, r *http.Request) {
	gameBoardID := r.PathValue("gameBoardID")
	gameBoard, ok := GameBoards[gameBoardID]
	if !ok {
		return
	}
	pid := getPidCookie(r)
	if pid == "" {
		slog.Error("ws connection pid is empty")
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("unable to upgrade ws connection", "err", err.Error())
		return
	}
	slog.Info("new ws connection.", "pid", pid, "gameboardID", gameBoardID)
	defer conn.Close()
	if gameBoard.PP1.ID == pid {
		if gameBoard.PP1.conn != nil {
			slog.Info("removing old ws connection for p1", "pid", gameBoard.PP1.ID, "gameboardID", gameBoardID)
			err := gameBoard.PP1.conn.Close()
			if err != nil {
				slog.Error(err.Error())
			}
			gameBoard.PP1.conn = nil
		}
		gameBoard.PP1.conn = conn
		if gameBoard.PP2.conn == nil {
			slog.Info("removing old ws connection for p2", "pid", gameBoard.PP1.ID, "gameboardID", gameBoardID)
			err := gameBoard.PP1.conn.WriteJSON(struct {
				Type string `json:"type"`
				Msg  string `json:"msg"`
			}{Type: "info", Msg: "waiting for other player to join"})
			if err != nil {
				slog.Error(err.Error())
			}
		}
	}
	if gameBoard.PP2.ID == pid {
		if gameBoard.PP2.conn != nil {
			err := gameBoard.PP2.conn.Close()
			if err != nil {
				slog.Error(err.Error())
			}
			gameBoard.PP2.conn = nil
		}
		gameBoard.PP2.conn = conn
		if gameBoard.PP1.conn == nil {
			err := gameBoard.PP2.conn.WriteJSON(struct {
				Type string `json:"type"`
				Msg  string `json:"msg"`
			}{Type: "info", Msg: "waiting for other player to join"})
			if err != nil {
				slog.Error(err.Error())
			}
		}
	}
	if gameBoard.PP1.conn != nil && gameBoard.PP2.conn != nil {
		response := GameStateResponse{Type: "game_state", State: gameBoard.State, NextMove: gameBoard.NextMove}
		err := gameBoard.PP1.conn.WriteJSON(response)
		if err != nil {
			slog.Error(err.Error())
		}
		err = gameBoard.PP2.conn.WriteJSON(response)
		if err != nil {
			slog.Error(err.Error())
		}
	}
	for {
		gs := board{}
		gb, ok := GameBoards[gameBoardID]
		if !ok {
			return
		}
		err := conn.ReadJSON(&gs)
		if err != nil {
			slog.Error("Error reading message:", "err", err.Error())
			break
		}
		if gb.NextMove == 1 && conn != gb.PP1.conn {
			continue
		}
		if gb.NextMove == 2 && conn != gb.PP2.conn {
			continue
		}
		UpdateMove(&gb.State, &gs)
		if gb.NextMove == 1 {
			gb.NextMove = 2
		} else {
			gb.NextMove = 1
		}
		result := Validate(gb.State)
		response := GameStateResponse{Type: "game_state", State: gb.State, Result: result, NextMove: gb.NextMove}
		if gb.PP1.conn != nil {
			err := gb.PP1.conn.WriteJSON(response)
			if err != nil {
				slog.Error(err.Error())
			}
		}
		if gb.PP2.conn != nil {
			err := gb.PP2.conn.WriteJSON(response)
			if err != nil {
				slog.Error(err.Error())
			}
		}
		closeHandler := conn.CloseHandler()
		conn.SetCloseHandler(func(code int, text string) error {
			err := closeHandler(code, text)
			if conn == gameBoard.PP1.conn {
				gameBoard.PP2.conn.WriteJSON(InfoResonse{Type: "info", Msg: "Other player got disconnected"})
				gameBoard.PP1.conn = nil
			}
			if conn == gameBoard.PP2.conn {
				gameBoard.PP1.conn.WriteJSON(InfoResonse{Type: "info", Msg: "Other player got disconnected"})
				gameBoard.PP2.conn = nil
			}
			return err
		})
	}
}

// validate takes current gameState and checks if any of the playerwinds the game
// if returns 3 kind of values
// 0: game still on
// -1: game is drawn
// 1: player 1 won
// 2: player 2 won
func Validate(gameState board) int {
	// check in rows for winner
	for i := 0; i < 9; i += 3 {
		if gameState[i] != 0 && gameState[i] == gameState[i+1] && gameState[i+1] == gameState[i+2] {
			return gameState[i]
		}
	}

	// check in cloumns for winner
	for i := 0; i < 3; i++ {
		if gameState[i] != 0 && gameState[i] == gameState[i+3] && gameState[i] == gameState[i+6] {
			return gameState[i]
		}
	}
	// check in diagonal for winner
	if gameState[0] != 0 && gameState[0] == gameState[4] && gameState[4] == gameState[8] {
		return gameState[0]
	}
	if gameState[2] != 0 && gameState[2] == gameState[4] && gameState[4] == gameState[6] {
		return gameState[0]
	}
	for i := 0; i < 9; i++ {
		if gameState[i] == 0 {
			return gameState[i]
		}
	}
	return -1
}

func UpdateMove(currentState *board, newState *board) {
	for i := 0; i < 9; i++ {
		if !(newState[i] == 1 || newState[i] == 2 || newState[i] == 0) {
			return
		}
		if currentState[i] == 0 && newState[i] != 0 {
			currentState[i] = newState[i]
			return
		}
	}
}

func main() {
	http.HandleFunc("GET /ws/{gameBoardID}", WShandler)
	http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		pid := getPidCookie(r)
		if pid == "" {
			newPlayerID := randomString(8)
			setPidCookie(w, newPlayerID)
		}
		tmpl.ExecuteTemplate(w, "index.html", nil)
	})
	http.HandleFunc("GET /gb/{gameBoardID}/reset", func(w http.ResponseWriter, r *http.Request) {
		gameBoardID := r.PathValue("gameBoardID")
		gameBoard, ok := GameBoards[gameBoardID]
		if !ok {
			http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
			return
		}
		gameBoard.State = board{}
		http.Redirect(w, r, fmt.Sprintf("/gb/%s", gameBoardID), http.StatusFound)
	})
	http.HandleFunc("GET /gb/{gameBoardID}", func(w http.ResponseWriter, r *http.Request) {
		gameBoardID := r.PathValue("gameBoardID")
		gameBoard, ok := GameBoards[gameBoardID]
		if !ok {
			http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
			return
		}
		pid := getPidCookie(r)
		playValue := 1
		if pid == "" {
			newPlayerID := randomString(8)
			setPidCookie(w, newPlayerID)
			gameBoard.PP2.ID = newPlayerID
			playValue = 2
		} else if pid != gameBoard.PP1.ID {
			gameBoard.PP2.ID = pid
			playValue = 2
		}
		tmpl.ExecuteTemplate(w, "gameboard.html", map[string]any{"GameBoardID": gameBoardID, "PlayValue": playValue, "State": gameBoard.State})
	})
	http.HandleFunc("POST /gb/new", func(w http.ResponseWriter, r *http.Request) {
		newGameBoardID := randomString(8)
		pid := getPidCookie(r)
		if pid == "" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		GameBoards[newGameBoardID] = &Game{State: board{}, PP1: Player{ID: pid}, NextMove: 1}
		slog.Info("new gameboard created.", "gameboardID", newGameBoardID)
		http.Redirect(w, r, fmt.Sprintf("/gb/%s", newGameBoardID), http.StatusFound)
	})
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	slog.Info("starting server", "addr", addr, "port", port)
	err := http.ListenAndServe(fmt.Sprintf("%s:%s", addr, port), nil)
	if err != nil {
		slog.Error(err.Error())
		panic(1)
	}
}

func setPidCookie(w http.ResponseWriter, value string) {
	expiration := time.Now().Add(365 * 24 * time.Hour)
	cookie := http.Cookie{Name: "pid", Value: value, Path: "/", Expires: expiration}
	http.SetCookie(w, &cookie)
}
func getPidCookie(r *http.Request) string {
	cookie, err := r.Cookie("pid")
	if err != nil {
		return ""
	}
	return cookie.Value
}
