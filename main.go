package main

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"io"
	"net/http"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
)

const (
	telegramBotToken  = "YOUR_TELEGRAM_BOT_TOKEN"
	natsURL           = "nats://localhost:4222"
	transcribeSubject = "voiceMsg"
	responseSubject   = "transcribedMsg"
)

type vMessage struct {
	ChatID       int64
	FileID       string
	UserID       int64
	VoiceMessage []byte
	Transcribed  string
}

func main() {
	// Initialize logger
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors:   true,
		FullTimestamp: true,
	})
	logrus.SetOutput(os.Stdout)
	logrus.SetLevel(logrus.DebugLevel)

	bot, err := tgbotapi.NewBotAPI(telegramBotToken)
	if err != nil {
		logrus.Panicln(err)
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	nc, err := nats.Connect(natsURL)
	if err != nil {
		logrus.Panicln(err)
	}

	// Subscribe to transcribed messages from NATS
	_, err = nc.Subscribe(responseSubject, func(msg *nats.Msg) {
		var vMsg vMessage
		err := bytesToStruct(msg.Data, &vMsg)
		if err != nil {
			logrus.Errorln("Failed to deserialize message:", err)
			return
		}

		logrus.Infof("Received transcribed message for ChatID %d", vMsg.ChatID)
		// Send the transcribed message back to the Telegram chat
		message := tgbotapi.NewMessage(vMsg.ChatID, vMsg.Transcribed)
		_, err = bot.Send(message)
		if err != nil {
			logrus.Errorln("Failed to send message to Telegram:", err)
		} else {
			logrus.Infoln("Transcribed message sent to Telegram")
		}
	})

	for update := range updates {
		if update.Message == nil {
			continue
		}
		if update.Message.Voice != nil {
			logrus.Infoln("Voice message received")
			handleVoiceMessage(nc, bot, update.Message)
		}

		if update.Message.IsCommand() {
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "")
			switch update.Message.Command() {
			case "start":
				msg.Text = "Type /help to see available commands"
			case "help":
				msg.Text = "Available commands:\n/whisper <message>"
			case "whisper":
			default:
				msg.Text = "I don't know that command"
			}
			_, err := bot.Send(msg)
			if err != nil {
				logrus.Errorln(err)
			}
		}
	}
}

func handleVoiceMessage(nc *nats.Conn, bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	fileID := message.Voice.FileID
	voiceFile, err := downloadVoiceFile(fileID, bot)
	if err != nil {
		logrus.Errorln(err)
		return
	}

	vMsg := vMessage{
		ChatID:       message.Chat.ID,
		FileID:       fileID,
		UserID:       message.From.ID,
		VoiceMessage: voiceFile,
	}

	vMsgBytes, err := structToBytes(vMsg)
	if err != nil {
		logrus.Errorln(err)
		return
	}

	err = nc.Publish(transcribeSubject, vMsgBytes)
	if err != nil {
		logrus.Errorln(err)
	}

	logrus.Infoln("Message sent to NATS")
}

// structToBytes serializes a struct to bytes
func structToBytes(data interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(data)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// bytesToStruct deserializes bytes to a struct
func bytesToStruct(data []byte, result interface{}) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)
	return dec.Decode(result)
}

func downloadVoiceFile(fileID string, bot *tgbotapi.BotAPI) ([]byte, error) {
	file, err := bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return nil, err
	}

	fileURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", telegramBotToken, file.FilePath)

	resp, err := http.Get(fileURL)
	if err != nil {
		logrus.Errorf("Failed to download voice file: %v", err)
		return nil, err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			logrus.Printf("Could't close the file")
		}
	}(resp.Body)

	voiceData, err := io.ReadAll(resp.Body)
	if err != nil {
		logrus.Errorf("Failed to read voice file: %v", err)
		return nil, err
	}

	return voiceData, nil
}
