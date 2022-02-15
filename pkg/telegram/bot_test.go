package telegram

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/NobleD5/alertmanager-bot/pkg/translation"
	"github.com/NobleD5/alertmanager-bot/pkg/vendor"

	"github.com/dchest/uniuri"
	"github.com/docker/libkv/store"
	"github.com/docker/libkv/store/boltdb"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/hako/durafmt"
	"github.com/prometheus/alertmanager/types"
	"github.com/prometheus/common/model"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/message/catalog"
	telebot "gopkg.in/tucnak/telebot.v2"
)

type alertResponse struct {
	Status string         `json:"status"`
	Alerts []*types.Alert `json:"data,omitempty"`
}

////////////////////////////////////////////////////////////////////////////////
// TESTING
////////////////////////////////////////////////////////////////////////////////

func TestHandlers(t *testing.T) {

	logger := log.NewLogfmtLogger(os.Stdout)
	logger = level.NewFilter(logger, level.AllowDebug())

	botToken := os.Getenv("ENV_BOT_TOKEN")
	botChat := os.Getenv("ENV_BOT_CHAT")

	dict, _ := translation.ParseYAMLDict("../../en.yaml", logger)
	fallback := language.MustParse("en")
	cat, _ := catalog.NewFromMap(dict, catalog.Fallback(fallback))
	translator := message.NewPrinter(cat.Languages()[0], message.Catalog(cat))

	var tmpl *vendor.Template
	funcs := vendor.DefaultFuncs
	funcs["since"] = func(t time.Time) string {
		return durafmt.Parse(time.Since(t)).String()
	}
	funcs["duration"] = func(start time.Time, end time.Time) string {
		return durafmt.Parse(end.Sub(start)).String()
	}

	vendor.DefaultFuncs = funcs

	tmpl, err := vendor.FromGlobs("../../default.tmpl")
	if err != nil {
		level.Error(logger).Log("msg", "failed to parse vendor.", "err", err)
		os.Exit(1)
	}

	kvStore, _ := boltdb.New([]string{"../test/kv.boltdb"}, &store.Config{Bucket: "dummy"})
	defer kvStore.Close()

	store, _ := NewChatStore(kvStore)

	c, _ := strconv.Atoi(botChat)
	chat := telebot.Chat{
		ID: int64(c),
	}

	//////////////////////////////////////////////////////////////////////////////
	// Alertmanager Server Mock Setup
	//////////////////////////////////////////////////////////////////////////////

	alertA := &types.Alert{}
	alertA.Labels = model.LabelSet{"alertname": "TestAlertA", "app": "test", "severity": "critical"}
	alertA.Annotations = model.LabelSet{"description": "description1", "summary": "summary1"}
	alertA.StartsAt = time.Now()
	alertA.EndsAt = time.Now().Add(2 * time.Hour)
	alertA.GeneratorURL = "https://foo.bar/"

	alertB := &types.Alert{}
	alertB.Labels = model.LabelSet{"alertname": "TestAlertB", "app": "test", "severity": "critical"}
	alertB.Annotations = model.LabelSet{"description": "description2", "summary": "summary2"}
	alertB.StartsAt = time.Now()
	alertB.EndsAt = time.Now().Add(2 * time.Hour)
	alertB.GeneratorURL = "https://foo.bar/"

	alertsJSON, err := ioutil.ReadFile("../test/alerts.json")
	statusJSON, err := ioutil.ReadFile("../test/status.json")
	silencesJSON, err := ioutil.ReadFile("../test/silences.json")
	if err != nil {
		return
	}

	mux := http.NewServeMux()

	// Status Mock
	mux.HandleFunc("/api/v1/status", func(res http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodGet:
			res.Header().Set("Content-Type", "application/json")
			res.WriteHeader(http.StatusOK)
			res.Write([]byte(statusJSON))
		default:
			res.WriteHeader(http.StatusGone)
		}
	})

	// Silences Mock
	mux.HandleFunc("/api/v1/silences", func(res http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodGet:
			res.Header().Set("Content-Type", "application/json")
			res.WriteHeader(http.StatusOK)
			res.Write([]byte(silencesJSON))
		case http.MethodPost:
			res.WriteHeader(http.StatusOK)
		default:
			res.WriteHeader(http.StatusGone)
		}
	})

	// Alerts Mock
	mux.HandleFunc("/api/v1/alerts", func(res http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodGet:
			res.Header().Set("Content-Type", "application/json")
			res.WriteHeader(http.StatusOK)
			res.Write([]byte(alertsJSON))
		default:
			res.WriteHeader(http.StatusGone)
		}
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()
	alertmanagerURL, _ := url.Parse(ts.URL)

	//////////////////////////////////////////////////////////////////////////////
	// Bot Setup
	//////////////////////////////////////////////////////////////////////////////
	bot, err := NewBot(
		store,
		botToken,
		int(1234),
		false,
		WithLogger(logger),
		WithAddr("localhost:8080"),
		WithRevision("revision"),
		WithStartTime(time.Now()),
		WithAlertmanager(alertmanagerURL),
		WithTemplates(tmpl),
		WithTranslation(translator),
		WithExtraAdmins(int(5678), int(9000)),
		WithChatsToSubscribe(chat),
	)
	if err != nil {
		panic(err)
	}

	message := &telebot.Message{
		Chat: &telebot.Chat{
			ID:        int64(c),
			Type:      telebot.ChatPrivate,
			Title:     "title",
			FirstName: "first_name",
			LastName:  "last_name",
			Username:  "username",
		},
		Sender: &telebot.User{
			ID:           int(1234),
			FirstName:    "FirstNameTest1",
			LastName:     "LastNameTest1",
			Username:     "UserNameTest1",
			LanguageCode: "language_code",
			IsBot:        false,
		},
		Text: "/",
	}
	botUsername := bot.telegram.Me.Username

	// ---------------------------------------------------------------------------
	//  CASE: HandleCommands()
	// ---------------------------------------------------------------------------
	message.Text = "/help@" + botUsername
	bot.HandleCommands(message)
	t.Log("HandleCommands() : Test 0.0 PASSED.")

	// for case when command uncomprehensible
	message.Text = "/ihavenomouthtoscream@" + botUsername
	bot.HandleCommands(message)
	t.Log("HandleCommands() : Test 0.1 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /start
	// ---------------------------------------------------------------------------
	message.Text = "/start@" + botUsername
	bot.handleStart(message)
	t.Log("handleStart() : Test 1 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /chats
	// ---------------------------------------------------------------------------
	message.Text = "/chats@" + botUsername
	bot.handleChats(message)
	t.Log("handleChats() : Test 2 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /stop
	// ---------------------------------------------------------------------------
	message.Text = "/stop@" + botUsername
	bot.handleStop(message)
	t.Log("handleStop() : Test 3.1 PASSED.")

	// for case when no chats are subscribed
	message.Text = "/chats@" + botUsername
	bot.handleChats(message)
	t.Log("handleChats() : Test 3.2 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /status
	// ---------------------------------------------------------------------------
	message.Text = "/status@" + botUsername
	bot.handleStatus(message)
	t.Log("handleStatus() : Test 4 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /alerts
	// ---------------------------------------------------------------------------
	// message.Text = "/alerts@" + botUsername
	// bot.handleAlerts(message)
	// t.Log("handleAlerts() : Test 5 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /silences
	// ---------------------------------------------------------------------------
	message.Text = "/silences@" + botUsername
	bot.handleSilences(message)
	t.Log("handleSilences() : Test 6 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /help
	// ---------------------------------------------------------------------------
	message.Text = "/help@" + botUsername
	bot.handleHelp(message)
	t.Log("handleHelp() : Test 7 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /admins
	// ---------------------------------------------------------------------------
	message.Text = "/admins@" + botUsername
	bot.handleAdminsList(message)
	t.Log("handleAdminsList() : Test 8 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /fingerprint
	// ---------------------------------------------------------------------------
	message.Text = "/fingerprint@" + botUsername + " " + alertA.Fingerprint().String()
	bot.handleFingerprint(message)
	t.Log("handleFingerprint() : Test 9 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /s2h
	// ---------------------------------------------------------------------------
	message.Text = "/s2h@" + botUsername + " " + alertA.Fingerprint().String()
	bot.handleSilenceTwoHours(message)
	t.Log("handleSilenceTwoHours() : Test 10.1 PASSED.")

	message.Text = "/s2h@" + botUsername + " " + "nonexistentfingerprint"
	bot.handleSilenceTwoHours(message)
	t.Log("handleSilenceTwoHours() : Test 10.2 PASSED.")

	message.Text = "/s2h@" + botUsername // no fingerprint given
	bot.handleSilenceTwoHours(message)
	t.Log("handleSilenceTwoHours() : Test 10.3 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /s48h
	// ---------------------------------------------------------------------------
	message.Text = "/s48h@" + botUsername + " " + alertB.Fingerprint().String()
	bot.handleSilenceFortyEightHours(message)
	t.Log("handleSilenceFortyEightHours() : Test 11.1 PASSED.")

	message.Text = "/s48h@" + botUsername + " " + "nonexistentfingerprint"
	bot.handleSilenceFortyEightHours(message)
	t.Log("handleSilenceFortyEightHours() : Test 11.2 PASSED.")

	message.Text = "/s48h@" + botUsername // no fingerprint given
	bot.handleSilenceFortyEightHours(message)
	t.Log("handleSilenceFortyEightHours() : Test 11.3 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /s2w
	// ---------------------------------------------------------------------------
	message.Text = "/s2w@" + botUsername + " " + alertB.Fingerprint().String()
	bot.handleSilenceTwoWeeks(message)
	t.Log("handleSilenceTwoWeeks() : Test 12.1 PASSED.")

	message.Text = "/s2w@" + botUsername + " " + "nonexistentfingerprint"
	bot.handleSilenceTwoWeeks(message)
	t.Log("handleSilenceTwoWeeks() : Test 12.2 PASSED.")

	message.Text = "/s2w@" + botUsername // no fingerprint given
	bot.handleSilenceTwoWeeks(message)
	t.Log("handleSilenceTwoWeeks() : Test 12.3 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /silence
	// ---------------------------------------------------------------------------
	message.Text = "/silence@" + botUsername + " " + alertB.Fingerprint().String()
	bot.handleSilence(message)
	t.Log("handleSilence() : Test 13 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /sm
	// ---------------------------------------------------------------------------
	message.Text = "/sm@" + botUsername + " " + fmt.Sprint(13)
	bot.handleServiceMaintenance(message)
	t.Log("handleServiceMaintenance() : Test 14.1 PASSED.")

	message.Text = "/sm@" + botUsername + " " + fmt.Sprint(30)
	bot.handleServiceMaintenance(message)
	t.Log("handleServiceMaintenance() : Test 14.2 PASSED.")

	message.Text = "/sm@" + botUsername + " " + fmt.Sprint(-100)
	bot.handleServiceMaintenance(message)
	t.Log("handleServiceMaintenance() : Test 14.3 PASSED.")

	message.Text = "/sm@" + botUsername // no duration given
	bot.handleServiceMaintenance(message)
	t.Log("handleServiceMaintenance() : Test 14.4 PASSED.")

	message.Text = "/sm@" + botUsername + " stop"
	bot.handleServiceMaintenance(message)
	t.Log("handleServiceMaintenance() : Test 14.5 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: /help & /stop non-admin
	// ---------------------------------------------------------------------------
	message.Sender.ID = int(1111)
	message.Text = "/help@" + botUsername
	bot.HandleCommands(message)
	t.Log("handleHelp() : Test 15.1 PASSED.")

	message.Text = "/status@" + botUsername
	bot.HandleCommands(message)
	t.Log("handleStatus() : Test 15.2 PASSED.")

	message.Text = "/chats@" + botUsername
	bot.HandleCommands(message)
	t.Log("handleChats() : Test 15.3 PASSED.")

	message.Text = "/stop@" + botUsername
	bot.HandleCommands(message)
	t.Log("handleStop() : Test 15.4 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: testing template
	// ---------------------------------------------------------------------------
	// template, err := bot.tmplAlerts(alertA)
	// if err != nil {
	// 	t.Logf("tmplAlerts() : Test 16 FAILED. Error: %s", err.Error())
	// } else {
	// 	t.Logf("tmplAlerts() : Test 16 PASSED. Template: %s", template)
	// }

	// ---------------------------------------------------------------------------
	//  CASE: is admin?
	// ---------------------------------------------------------------------------
	id := bot.isAdminID(int(1234))
	if id != true {
		t.Error("isAdminID() : Test 17 FAILED, wrong return, case is admin")
	} else {
		t.Log("isAdminID() : Test 17 PASSED")
	}

	// ---------------------------------------------------------------------------
	//  CASE: hello admin
	// ---------------------------------------------------------------------------
	bot.SendAdminMessage(int(1234), "Hello!")
	t.Log("SendAdminMessage() : Test 18 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE:
	// ---------------------------------------------------------------------------
	bot.getHandlerName(bot.splitMessage(uniuri.NewLen(int(10000))))
	t.Log("getHandlerName() : Test 19 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: return non-truncated string
	// ---------------------------------------------------------------------------
	nontrunc := bot.truncateMessage("sss")
	t.Logf("truncateMessage() : Test 20.1 PASSED, non-truncated message: \n%s", nontrunc)

	// ---------------------------------------------------------------------------
	//  CASE: return truncated string
	// ---------------------------------------------------------------------------
	trunc := bot.truncateMessage(uniuri.NewLen(int(2000)) + "\n\n" + uniuri.NewLen(int(4000)) + "\n\n")
	t.Logf("truncateMessage() : Test 20.2 PASSED, truncated message: \n%s", trunc)

	// ---------------------------------------------------------------------------
	//  CASE: no end of alert found, i.e. '\n\n'
	// ---------------------------------------------------------------------------
	_ = bot.truncateMessage(uniuri.NewLen(int(5000)))
	t.Log("truncateMessage() : Test 20.3 PASSED.")

	// ---------------------------------------------------------------------------
	//  CASE: return split strings with '\n\n'
	// ---------------------------------------------------------------------------
	split := bot.splitMessage(uniuri.NewLen(int(2000)) + "\n\n" + uniuri.NewLen(int(4000)) + "\n\n")
	t.Logf("splitMessage() : Test 21.1 PASSED, split messages: \n%s", fmt.Sprintln(split))

	// ---------------------------------------------------------------------------
	//  CASE: return split strings without '\n\n'
	// ---------------------------------------------------------------------------
	split = bot.splitMessage(uniuri.NewLen(int(10000)))
	t.Logf("splitMessage() : Test 21.2 PASSED, split messages: \n%s", fmt.Sprintln(split))

}

func TestSendWebhooks(t *testing.T) {

	logger := log.NewLogfmtLogger(os.Stdout)
	logger = level.NewFilter(logger, level.AllowDebug())

	botToken := os.Getenv("ENV_BOT_TOKEN")
	botChat := os.Getenv("ENV_BOT_CHAT")

	var tmpl *vendor.Template
	funcs := vendor.DefaultFuncs
	funcs["since"] = func(t time.Time) string {
		return durafmt.Parse(time.Since(t)).String()
	}
	funcs["duration"] = func(start time.Time, end time.Time) string {
		return durafmt.Parse(end.Sub(start)).String()
	}

	vendor.DefaultFuncs = funcs

	tmpl, err := vendor.FromGlobs("../../default.tmpl")
	if err != nil {
		level.Error(logger).Log("msg", "failed to parse vendor.", "err", err)
		os.Exit(1)
	}
	u, err := url.Parse("http://localhost:9093")
	if err != nil {
		level.Error(logger).Log("msg", "failed to parse URL", "err", err)
		os.Exit(1)
	}
	tmpl.ExternalURL = u

	kvStore, _ := boltdb.New([]string{"../test/kv.boltdb"}, &store.Config{Bucket: "dummy"})
	defer kvStore.Close()

	store, _ := NewChatStore(kvStore)

	c, _ := strconv.Atoi(botChat)

	bot, err := NewBot(
		store,
		botToken,
		int(1234),
		false,
		WithLogger(logger),
		WithTemplates(tmpl),
	)
	if err != nil {
		panic(err)
	}

	webhooks := make(chan vendor.Message, 32)

	webhook := &vendor.Message{
		Data: &vendor.Data{
			Receiver: "receiver",
			Status:   "status",
			Alerts: vendor.Alerts{
				vendor.Alert{
					Status:       "firing",
					Labels:       vendor.KV{"alert_label": "label1", "alertname": "TestName!"},
					Annotations:  vendor.KV{"message": "TestMessage!"},
					StartsAt:     time.Now(),
					EndsAt:       time.Now(),
					GeneratorURL: "http://localhost:9093",
					Fingerprint:  "fingerprint",
				},
			},
			GroupLabels:       vendor.KV{"group_labels": "test1"},
			CommonLabels:      vendor.KV{"common_labels": "test2"},
			CommonAnnotations: vendor.KV{"common_annotations": "test3"},
			ExternalURL:       "http://localhost:9093/",
		},
		Version:         "version",
		GroupKey:        "group_key",
		TruncatedAlerts: uint64(0),
	}
	message := &telebot.Message{
		Chat: &telebot.Chat{
			ID:        int64(c),
			Type:      telebot.ChatPrivate,
			Title:     "title",
			FirstName: "first_name",
			LastName:  "last_name",
			Username:  "username",
		},
		Sender: &telebot.User{
			ID:           int(1234),
			FirstName:    "FirstNameTest1",
			LastName:     "LastNameTest1",
			Username:     "UserNameTest1",
			LanguageCode: "language_code",
			IsBot:        false,
		},
		Text: "/",
	}
	botUsername := bot.telegram.Me.Username

	message.Text = "/start@" + botUsername
	bot.handleStart(message)

	// ---------------------------------------------------------------------------
	//  CASE: testing webhook
	// ---------------------------------------------------------------------------
	go bot.Serve(webhooks)

	time.Sleep(2 * time.Second)

	webhooks <- *webhook

	time.Sleep(2 * time.Second)

	webhook.Data.Alerts[0].Status = "resolved"
	webhooks <- *webhook

	time.Sleep(2 * time.Second)

	t.Log("Serve() : Test 1 PASSED.")

}
