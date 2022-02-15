package telegram

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NobleD5/alertmanager-bot/pkg/alertmanager"
	"github.com/NobleD5/alertmanager-bot/pkg/vendor"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/hako/durafmt"
	"github.com/prometheus/alertmanager/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/text/language"
	loc "golang.org/x/text/message"
	telebot "gopkg.in/tucnak/telebot.v2"
)

const (
	commandStart = "/start"
	commandStop  = "/stop"
	commandHelp  = "/help"
	commandChats = "/chats"

	commandStatus   = "/status"
	commandAlerts   = "/alerts"
	commandSilences = "/silences"

	commandSilenceFor2Hours  = "/s2h"
	commandSilenceFor48Hours = "/s48h"
	commandSilenceFor2Weeks  = "/s2w"

	commandServiceMaintenance = "/sm"

	commandSilence    = "/silence"
	commandSilenceAdd = "/silence_add"
	commandSilenceDel = "/silence_del"

	commandFingerprint = "/fingerprint"
	commandAdmins      = "/admins"
)

// BotChatStore is all the Bot needs to store and read
type BotChatStore interface {
	List() ([]telebot.Chat, error)
	Add(telebot.Chat) error
	Remove(telebot.Chat) error
}

// Bot runs the alertmanager telegram
type Bot struct {
	addr         string
	admins       []int // must be kept sorted
	alertmanager *url.URL
	templates    *vendor.Template
	chatStore    BotChatStore
	logger       log.Logger
	revision     string
	startTime    time.Time

	translator *loc.Printer

	telegram *telebot.Bot

	commandsCounter *prometheus.CounterVec
	webhooksCounter prometheus.Counter
}

// BotOption passed to NewBot to change the default instance
type BotOption func(b *Bot)

// NewBot creates a Bot with the UserStore and telegram telegram
func NewBot(chatStore BotChatStore, token string, admin int, verbose bool, opts ...BotOption) (*Bot, error) {

	bot, err := telebot.NewBot(
		telebot.Settings{
			Token:   token,
			Poller:  &telebot.LongPoller{Timeout: 1 * time.Second},
			Verbose: verbose,
			// offline: true,
		},
	)
	if err != nil {
		return nil, err
	}

	reg := prometheus.NewRegistry()
	commandsCounter := promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
		Namespace: "alertmanagerbot",
		Name:      "commands_total",
		Help:      "Number of commands received by command name",
	}, []string{"command"})
	// if err := prometheus.Register(commandsCounter); err != nil {
	// 	return nil, err
	// }

	b := &Bot{
		logger:          log.NewNopLogger(),
		translator:      loc.NewPrinter(language.English),
		telegram:        bot,
		chatStore:       chatStore,
		addr:            "127.0.0.1:8080",
		admins:          []int{admin},
		alertmanager:    &url.URL{Host: "localhost:9093"},
		commandsCounter: commandsCounter,
		// TODO: initialize templates with default?
	}

	for _, opt := range opts {
		opt(b)
	}

	return b, nil
}

// WithLogger sets the logger for the Bot as an option
func WithLogger(l log.Logger) BotOption {
	return func(b *Bot) {
		b.logger = l
	}
}

// WithAddr sets the internal listening addr of the bot's web server receiving webhooks
func WithAddr(addr string) BotOption {
	return func(b *Bot) {
		b.addr = addr
	}
}

// WithAlertmanager sets the connection url for the Alertmanager
func WithAlertmanager(u *url.URL) BotOption {
	return func(b *Bot) {
		b.alertmanager = u
	}
}

// WithTemplates uses Alertmanager template to render messages for Telegram
func WithTemplates(t *vendor.Template) BotOption {
	return func(b *Bot) {
		b.templates = t
	}
}

// WithTranslation sets translation for Telegram messages
func WithTranslation(t *loc.Printer) BotOption {
	return func(b *Bot) {
		b.translator = t
	}
}

// WithRevision is setting the Bot's revision for status commands
func WithRevision(r string) BotOption {
	return func(b *Bot) {
		b.revision = r
	}
}

// WithStartTime is setting the Bot's start time for status commands
func WithStartTime(st time.Time) BotOption {
	return func(b *Bot) {
		b.startTime = st
	}
}

// WithExtraAdmins allows the specified additional user IDs to issue admin
// commands to the bot.
func WithExtraAdmins(ids ...int) BotOption {
	return func(b *Bot) {
		b.admins = append(b.admins, ids...)
		sort.Ints(b.admins)
	}
}

// WithChatsToSubscribe allows initial subscribe for listed chats ID
func WithChatsToSubscribe(chats ...telebot.Chat) BotOption {
	return func(b *Bot) {
		for _, chat := range chats {
			if err := b.chatStore.Add(chat); err != nil {
				level.Warn(b.logger).Log("msg", "failed to add chat to chat store", "err", err)
			}
		}
	}
}

// Start functions just wrap Telegram Bot Start
func (b *Bot) Start() {
	b.telegram.Start()
}

// Stop functions just wrap Telegram Bot Stop
func (b *Bot) Stop() {
	b.telegram.Stop()
}

// Handle functions just wrap Telegram Handle
func (b *Bot) Handle(endpoint interface{}, handler interface{}) {
	b.telegram.Handle(endpoint, handler)
}

// Serve listen for webhook messages from AlertManager and send them to the telegram
func (b *Bot) Serve(webhooks <-chan vendor.Message) {

	for {
		select {

		case w := <-webhooks:

			level.Info(b.logger).Log("msg", "received webhook from Alertmanager")

			chats, err := b.chatStore.List()
			if err != nil {
				level.Error(b.logger).Log("msg", "failed to get chat list from store", "err", err)
				continue
			}

			data := &vendor.Data{
				Receiver:          w.Receiver,
				Status:            w.Status,
				Alerts:            w.Alerts,
				GroupLabels:       w.GroupLabels,
				CommonLabels:      w.CommonLabels,
				CommonAnnotations: w.CommonAnnotations,
				ExternalURL:       w.ExternalURL,
			}

			out, err := b.templates.ExecuteHTMLString(`{{ template "telegram.default" . }}`, data)
			if err != nil {
				level.Warn(b.logger).Log("msg", "failed to template alerts", "err", err)
				continue
			}

			for _, chat := range chats {
				for _, splitedMessage := range b.splitMessage(out) {
					_, err = b.telegram.Send(&chat, splitedMessage, &telebot.SendOptions{ParseMode: telebot.ModeHTML})
					if err != nil {
						level.Warn(b.logger).Log("msg", "failed to send message to subscribed chat", "err", err)
					} else {
						level.Debug(b.logger).Log("msg", "send this Telegram", "message", splitedMessage)
					}
				}
			}

		default:
		}
	}

}

// SendAdminMessage to the admin's ID with a message
func (b *Bot) SendAdminMessage(adminID int, message string) {
	b.telegram.Send(&telebot.User{ID: adminID}, message)
}

// HandleCommands process received commands via Telegram Message
func (b *Bot) HandleCommands(message *telebot.Message) {

	commandSuffix := fmt.Sprintf("@%s", b.telegram.Me.Username)

	commands := map[string]func(message *telebot.Message){
		commandStart:              b.handleStart,
		commandStop:               b.handleStop,
		commandHelp:               b.handleHelp,
		commandChats:              b.handleChats,
		commandStatus:             b.handleStatus,
		commandAlerts:             b.handleAlerts,
		commandSilences:           b.handleSilences,
		commandSilence:            b.handleSilence,
		commandSilenceFor2Hours:   b.handleSilenceTwoHours,
		commandSilenceFor48Hours:  b.handleSilenceFortyEightHours,
		commandSilenceFor2Weeks:   b.handleSilenceTwoWeeks,
		commandServiceMaintenance: b.handleServiceMaintenance,
		commandFingerprint:        b.handleFingerprint,
		commandAdmins:             b.handleAdminsList,
	}

	// init counters with 0
	for command := range commands {
		b.commandsCounter.WithLabelValues(command).Add(0)
	}

	if message.IsService() {
		level.Debug(b.logger).Log("msg", "received 'service message'", "text", message.Text)
		return
	}

	if err := b.telegram.Notify(message.Chat, telebot.Typing); err != nil {
		level.Error(b.logger).Log("msg", "error sending telegram message", "err", err)
		return
	}

	level.Debug(b.logger).Log("msg", "message received", "text", message.Text)

	// Remove the command suffix from the text, /help@BotName => /help
	commandName := strings.Replace(message.Text, commandSuffix, "", -1)
	// Only take the first part into account, /help foo => /help
	commandName = strings.Split(commandName, " ")[0]

	level.Debug(b.logger).Log("msg", "command received", "command", commandName)

	if !b.isAdminID(message.Sender.ID) && !(commandName == "/help" || commandName == "/status" || commandName == "/chats") {
		b.commandsCounter.WithLabelValues("dropped").Inc()
		level.Error(b.logger).Log("msg", "dropped message from forbidden sender")

		b.telegram.Reply(
			message,
			b.translator.Sprintf("responseNonAdmin", message.Sender.Username, message.Sender.FirstName, message.Sender.LastName),
		)

		return
	}

	// Get the corresponding handler from the map by the commands text
	handler, ok := commands[commandName]

	if !ok {
		b.commandsCounter.WithLabelValues("incomprehensible").Inc()
		b.telegram.Reply(
			message,
			b.translator.Sprintf("responseIncomprehensible"),
		)
		return
	}

	level.Debug(b.logger).Log("msg", "handler identified", "handler", fmt.Sprint(b.getHandlerName(handler)))

	b.commandsCounter.WithLabelValues(commandName).Inc()
	handler(message)

}

//
func (b *Bot) handleStart(message *telebot.Message) {

	if err := b.chatStore.Add(*message.Chat); err != nil {
		level.Warn(b.logger).Log("msg", "failed to add chat to chat store", "err", err)
		b.telegram.Send(message.Chat, b.translator.Sprintf("responseStartFail"))
		return
	}

	b.telegram.Send(message.Chat, b.translator.Sprintf("responseStart", message.Sender.FirstName, commandHelp))
	level.Info(b.logger).Log(
		"msg", "user subscribed",
		"username", message.Sender.Username,
		"user_id", message.Sender.ID,
		"admin", b.isAdminID(message.Sender.ID),
	)

}

//
func (b *Bot) handleStop(message *telebot.Message) {

	if err := b.chatStore.Remove(*message.Chat); err != nil {
		level.Warn(b.logger).Log("msg", "failed to remove chat from chat store", "err", err)
		b.telegram.Send(message.Chat, b.translator.Sprintf("responseStopFail"))
		return
	}

	b.telegram.Send(message.Chat, b.translator.Sprintf("responseStop", message.Sender.FirstName, commandHelp))
	level.Info(b.logger).Log(
		"msg", "user unsubscribed",
		"username", message.Sender.Username,
		"user_id", message.Sender.ID,
		"admin", b.isAdminID(message.Sender.ID),
	)

}

//
func (b *Bot) handleHelp(message *telebot.Message) {
	b.telegram.Send(
		message.Chat,
		b.translator.Sprintf("responseHelp",
			commandStart,
			commandStop,
			commandStatus,
			commandAlerts,
			commandSilences,
			commandSilence,
			commandSilenceFor2Hours,
			commandSilenceFor48Hours,
			commandSilenceFor2Weeks,
			commandServiceMaintenance,
			commandChats,
		),
		&telebot.SendOptions{ParseMode: telebot.ModeMarkdown},
	)
	level.Info(b.logger).Log(
		"msg", "user requested help",
		"username", message.Sender.Username,
		"user_id", message.Sender.ID,
		"admin", b.isAdminID(message.Sender.ID),
	)

}

//
func (b *Bot) handleChats(message *telebot.Message) {

	chats, err := b.chatStore.List()
	if err != nil {
		level.Warn(b.logger).Log("msg", "failed to list chats from chat store", "err", err)
		b.telegram.Send(message.Chat, b.translator.Sprintf("responseChatsFail"))
		return
	}

	list := ""
	for _, chat := range chats {
		switch chat.Type {
		case "group":
			list = list + fmt.Sprintf("@%s\n", chat.Title)
		case "private":
			list = list + fmt.Sprintf("private chat\n")
		case "privatechannel":
			list = list + fmt.Sprintf("private channel\n")
		default:
			list = list + fmt.Sprintf("@%s (%s %s)\n", chat.Username, chat.FirstName, chat.LastName)
		}
	}

	b.telegram.Send(message.Chat, b.translator.Sprintf("responseChats", list))
	level.Info(b.logger).Log(
		"msg", "user requested chats list",
		"username", message.Sender.Username,
		"user_id", message.Sender.ID,
		"admin", b.isAdminID(message.Sender.ID),
	)

}

//
func (b *Bot) handleStatus(message *telebot.Message) {

	s, err := alertmanager.Status(b.logger, b.alertmanager.String())
	if err != nil {
		level.Warn(b.logger).Log("msg", "failed to get status", "err", err)
		b.telegram.Send(message.Chat, b.translator.Sprintf("responseStatusFail", err))
		return
	}

	uptime := durafmt.Parse(time.Since(s.Data.Uptime))
	uptimeBot := durafmt.Parse(time.Since(b.startTime))

	b.telegram.Send(
		message.Chat,
		b.translator.Sprintf(
			"responseStatus",
			s.Data.VersionInfo.Version,
			uptime,
			b.revision,
			uptimeBot,
		),
		&telebot.SendOptions{ParseMode: telebot.ModeMarkdown},
	)
	level.Info(b.logger).Log(
		"msg", "user requested status",
		"username", message.Sender.Username,
		"user_id", message.Sender.ID,
		"admin", b.isAdminID(message.Sender.ID),
	)

}

//
func (b *Bot) handleAlerts(message *telebot.Message) {

	alerts, err := alertmanager.ListAlerts(b.logger, b.alertmanager.String())
	if err != nil {
		b.telegram.Send(message.Chat, b.translator.Sprintf("responseAlertsFail", err))
		level.Error(b.logger).Log("msg", "failed to get alerts", "err", err)
		return
	}
	level.Debug(b.logger).Log("alerts", fmt.Sprint(alerts))

	if len(alerts) == 0 {
		b.telegram.Send(message.Chat, b.translator.Sprintf("responseNoAlerts"))
		return
	}

	out, err := b.tmplAlerts(alerts...)
	if err != nil {
		b.telegram.Send(message.Chat, b.translator.Sprintf("responseAlertsFail", err))
		level.Error(b.logger).Log("msg", "failed to template alerts", "err", err)
		return
	}
	level.Debug(b.logger).Log("template", fmt.Sprint(out))

	for _, splitedMessage := range b.splitMessage(out) {
		_, err = b.telegram.Send(message.Chat, splitedMessage, &telebot.SendOptions{
			ParseMode: telebot.ModeHTML,
		})
		if err != nil {
			level.Warn(b.logger).Log("msg", "failed to send list of alerts", "err", err)
		}
	}

}

//
func (b *Bot) handleSilences(message *telebot.Message) {

	silences, err := alertmanager.ListSilences(b.logger, b.alertmanager.String())
	if err != nil {
		b.telegram.Send(message.Chat, b.translator.Sprintf("responseSilencesFail", err))
		level.Error(b.logger).Log("msg", "failed to get silences", "err", err)
		return
	}

	if len(silences) == 0 {
		b.telegram.Send(message.Chat, b.translator.Sprintf("responseNoSilences"))
		return
	}

	var out string
	for _, silence := range silences {
		out = out + alertmanager.SilenceMessage(silence) + "\n"
	}

	for _, splitedMessage := range b.splitMessage(out) {
		_, err = b.telegram.Send(message.Chat, splitedMessage, &telebot.SendOptions{
			ParseMode: telebot.ModeMarkdown})
		if err != nil {
			level.Warn(b.logger).Log("msg", "failed to send list of silences", "err", err)
		}
	}

}

// TODO intellectual silence
func (b *Bot) handleSilence(message *telebot.Message) {

	b.telegram.Reply(
		message, b.translator.Sprintf("responseInDev", " 🖕"),
	)

}

// Fast silencing alert for 2 hours
func (b *Bot) handleSilenceTwoHours(message *telebot.Message) {

	const time = 2 * time.Hour

	fingerPrint := ""

	if strings.Index(message.Text, " ") != -1 {
		fingerPrint = strings.Split(message.Text, " ")[1]
		err := b.silence(fingerPrint, time)
		if err != nil {
			b.telegram.Reply(message, b.translator.Sprintf("responseSilenceFail", err))
			return
		}
		b.telegram.Reply(message, b.translator.Sprintf("responseSilenceCreated"))
	} else {
		b.telegram.Reply(message, b.translator.Sprintf("responseNoFingerprint"))
	}

}

// Fast silencing alert for 48 hours
func (b *Bot) handleSilenceFortyEightHours(message *telebot.Message) {

	const time = 48 * time.Hour

	fingerPrint := ""

	if strings.Index(message.Text, " ") != -1 {
		fingerPrint = strings.Split(message.Text, " ")[1]
		err := b.silence(fingerPrint, time)
		if err != nil {
			b.telegram.Reply(message, b.translator.Sprintf("responseSilenceFail", err))
			return
		}
		b.telegram.Reply(message, b.translator.Sprintf("responseSilenceCreated"))
	} else {
		b.telegram.Reply(message, b.translator.Sprintf("responseNoFingerprint"))
	}

}

// Fast silencing alert for 2 weeks
func (b *Bot) handleSilenceTwoWeeks(message *telebot.Message) {

	const time = 336 * time.Hour

	fingerPrint := ""

	if strings.Index(message.Text, " ") != -1 {
		fingerPrint = strings.Split(message.Text, " ")[1]
		err := b.silence(fingerPrint, time)
		if err != nil {
			b.telegram.Reply(message, b.translator.Sprintf("responseSilenceFail", err))
			return
		}
		b.telegram.Reply(message, b.translator.Sprintf("responseSilenceCreated"))
	} else {
		b.telegram.Reply(message, b.translator.Sprintf("responseNoFingerprint"))
	}

}

// Control silencing/expire of ALL alerts for 8 hour (or custom) maintenance
func (b *Bot) handleServiceMaintenance(message *telebot.Message) {

	const defaultTime = 8 * time.Hour

	if strings.Index(message.Text, " ") != -1 {

		switch strings.Split(message.Text, " ")[1] {
		case "stop":
			// Custom DELETE request
			err := alertmanager.DeleteSuperSilence(b.logger, b.alertmanager.String(), "SUPER_SILENCE")
			if err != nil {
				b.telegram.Reply(message, b.translator.Sprintf("responseSilenceFail", err))
				return
			}
			b.telegram.Reply(message, "TEST_SUPERSTOP")
		default:
			newTime, err := strconv.Atoi(strings.Split(message.Text, " ")[1])
			if newTime > 24 || newTime < 1 {
				newTime = 8
			}
			err = b.silenceAll(time.Duration(newTime) * time.Hour)
			if err != nil {
				b.telegram.Reply(message, b.translator.Sprintf("responseSilenceFail", err))
				return
			}
			b.telegram.Reply(message, b.translator.Sprintf("responseSilenceAllCreated", fmt.Sprint(newTime)))
		}

	} else {
		err := b.silenceAll(defaultTime)
		if err != nil {
			b.telegram.Reply(message, b.translator.Sprintf("responseSilenceFail", err))
			return
		}
		b.telegram.Reply(message, b.translator.Sprintf("responseSilenceAllCreated", fmt.Sprint(defaultTime.Hours())))
	}

}

//
func (b *Bot) handleFingerprint(message *telebot.Message) {

	fingerPrint := ""
	count := 0

	if strings.Index(message.Text, " ") != -1 {

		fingerPrint = strings.Split(message.Text, " ")[1]

		alerts, err := alertmanager.ListAlerts(b.logger, b.alertmanager.String())
		if err != nil {
			b.telegram.Send(message.Chat, b.translator.Sprintf("responseAlertsFail", err))
			level.Error(b.logger).Log("msg", "failed to get alerts", "err", err)
			return
		}

		for _, alert := range alerts {
			if alert.Fingerprint().String() == fingerPrint {
				count++
				level.Debug(b.logger).Log("msg", "found alert match", "string", alert.String())
				b.telegram.Reply(
					message, b.translator.Sprintf("responseFingerprintFound", alert.String(), alert.Labels.String(), fingerPrint),
				)
				break
			}
		}

		if count == 0 {
			b.telegram.Reply(
				message, b.translator.Sprintf("responseNoFingerprintFound"),
			)
		}

	}
}

// Show current administrators list
func (b *Bot) handleAdminsList(message *telebot.Message) {

	var (
		list  = ""
		count = 0
	)

	for _, admin := range b.admins {

		member, err := b.telegram.ChatMemberOf(message.Chat, &telebot.User{ID: admin})
		if err != nil {
			level.Error(b.logger).Log("msg", "failed to get member data", "err", err)
		} else {
			count++
			list += "Username: *" + member.User.Username + "* -- " + member.User.FirstName + " " + member.User.LastName + " (" + fmt.Sprint(member.User.ID) + ")\n"
		}

	}
	level.Debug(b.logger).Log("msg", "admins", "list", list, "count", count)

	b.telegram.Reply(
		message,
		b.translator.Sprintf("responseAdmins", list),
		&telebot.SendOptions{ParseMode: telebot.ModeMarkdown},
	)

}

// silence is used for making predefined in duration silences.
func (b *Bot) silence(fingerPrint string, duration time.Duration) error {

	var (
		silence  *vendor.Silence
		matchers []*vendor.Matcher
		count    int
	)

	level.Debug(b.logger).Log("fingerprint", fingerPrint)
	level.Debug(b.logger).Log("duration", duration)

	alerts, err := alertmanager.ListAlerts(b.logger, b.alertmanager.String())
	if err != nil {
		level.Error(b.logger).Log("msg", "failed to get alerts", "err", err)
		return err
	}
	level.Debug(b.logger).Log("alerts", fmt.Sprint(alerts))

	if len(alerts) == 0 {
		level.Error(b.logger).Log("msg", "no alerts found right now")
		return errors.New("no alerts found right now")
	}
	level.Debug(b.logger).Log("msg", "alerts", "len", len(alerts))

	count = 0
	for _, alert := range alerts {

		if alert.Fingerprint().String() == fingerPrint {

			level.Debug(b.logger).Log("msg", "found alert match", "labels", alert.Labels.String())

			matchers, err = vendor.ParseMatchers(alert.Labels.String())
			if err != nil {
				level.Error(b.logger).Log("msg", "failed to parse alert labels into matchers", "err", err)
				return err
			}
			level.Debug(b.logger).Log("msg", "parsed", "matchers", fmt.Sprint(matchers))
			// Assemble new silence
			silence = &vendor.Silence{
				ID:        "",
				Matchers:  matchers,
				StartsAt:  time.Now(),
				EndsAt:    time.Now().Add(duration),
				UpdatedAt: time.Now(),
				CreatedBy: "alertmanager-bot",
				Comment:   "Enacted by administrator command",
				Status:    vendor.SilenceStatus{State: vendor.CalcSilenceState(time.Now(), time.Now().Add(duration))},
			}
			// Custom POST request
			return alertmanager.PostSilence(b.logger, b.alertmanager.String(), *silence)
		} else {
			count++
			level.Debug(b.logger).Log("msg", "no matches with current alert", "count", count)
		}

	}

	if count == len(alerts) {
		level.Error(b.logger).Log("msg", "no matches found for silence", "count", count)
		return errors.New("no matches found for silence!")
	}

	return nil
}

// silenceAll is used for making predefined in duration silence for ALL alerts (past, present and future).
func (b *Bot) silenceAll(duration time.Duration) error {

	var (
		silence  *vendor.Silence
		matchers []*vendor.Matcher
	)

	superMatch := "alertname=~\".+\""
	matchers, _ = vendor.ParseMatchers(superMatch)
	level.Debug(b.logger).Log("msg", "parsed", "matchers", fmt.Sprint(matchers))
	// Assemble new silence
	silence = &vendor.Silence{
		ID:        "",
		Matchers:  matchers,
		StartsAt:  time.Now(),
		EndsAt:    time.Now().Add(duration),
		UpdatedAt: time.Now(),
		CreatedBy: "alertmanager-bot",
		Comment:   "Enacted by administrator command",
		Status:    vendor.SilenceStatus{State: vendor.CalcSilenceState(time.Now(), time.Now().Add(duration))},
	}
	// Custom POST request
	return alertmanager.PostSilence(b.logger, b.alertmanager.String(), *silence)
}

// isAdminID returns whether id is one of the configured admin IDs.
func (b *Bot) isAdminID(id int) bool {
	i := sort.SearchInts(b.admins, id)
	return i < len(b.admins) && b.admins[i] == id
}

// Apply template (Alert -> string)
func (b *Bot) tmplAlerts(alerts ...*types.Alert) (string, error) {

	data := b.templates.Data("default", nil, alerts...)
	level.Debug(b.logger).Log("data", fmt.Sprint(data))
	out, err := b.templates.ExecuteHTMLString(`{{ template "telegram.default" . }}`, data)
	if err != nil {
		level.Warn(b.logger).Log("msg", "failed to parse provided template", "err", err)
		return "", err
	}

	return out, nil
}

// SplitMessage splits string into slice of 4095 bytes strings
func (b *Bot) splitMessage(str string) []string {

	const maxLength = 4095
	splits := []string{}

	if len(str) > 4095 { // telegram API can only support 4096 bytes per message
		startIndex := 0
		for (startIndex + maxLength) < len(str) {
			strBounds := (str[startIndex:(startIndex + maxLength)])
			lastIndex := strings.LastIndex(strBounds, "\n\n")
			if lastIndex != -1 {
				level.Debug(b.logger).Log("msg", "Index found", "index", lastIndex)
				split := (str[startIndex:(startIndex + lastIndex)])
				splits = append(splits, split)
				startIndex += lastIndex
			} else {
				level.Warn(b.logger).Log("msg", "Index not found, proceeding without it.")
				splits = append(splits, strBounds)
				startIndex += maxLength
			}
		}
	} else {
		level.Warn(b.logger).Log("msg", "Message is lesser than 4095, skipping split.")
		splits = append(splits, str)
	}

	return splits
}

// Truncate very big message
func (b *Bot) truncateMessage(str string) string {

	truncateMsg := str
	if len(str) > 4095 { // telegram API can only support 4096 bytes per message
		level.Warn(b.logger).Log("msg", "Message is bigger than 4095, truncate...")
		// find the end of last alert, we do not want break the html tags
		i := strings.LastIndex(str[0:4090], "\n\n") // 4090 + "\n..." == 4095
		if i > 1 {
			truncateMsg = str[0:i] + "\n..."
		} else {
			truncateMsg = "Message is too long... can't send.."
			level.Warn(b.logger).Log("msg", "Unable to find the end of last alert.")
		}
		return truncateMsg
	}
	level.Warn(b.logger).Log("msg", "Message is lesser than 4095, skipping truncate.")

	return truncateMsg
}

// Get handler name for DEBUG purposes
func (b *Bot) getHandlerName(i interface{}) string {
	return runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
}
