package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/NobleD5/alertmanager-bot/pkg/alertmanager"
	"github.com/NobleD5/alertmanager-bot/pkg/telegram"
	"github.com/NobleD5/alertmanager-bot/pkg/translation"
	"github.com/NobleD5/alertmanager-bot/pkg/vendor"

	"github.com/docker/libkv/store"
	"github.com/docker/libkv/store/boltdb"
	"github.com/docker/libkv/store/consul"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/hako/durafmt"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/message/catalog"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
	telebot "gopkg.in/tucnak/telebot.v2"
)

const (
	storeBolt   = "bolt"
	storeConsul = "consul"

	levelDebug = "debug"
	levelInfo  = "info"
	levelWarn  = "warn"
	levelError = "error"
)

var (
	// Version of alertmanager-bot.
	Version string
	// Revision or Commit this binary was built from.
	Revision string
	// BuildDate this binary was built.
	BuildDate string
	// GoVersion running this binary.
	GoVersion = runtime.Version()
	// StartTime has the time this was started.
	StartTime = time.Now()

	chats []telebot.Chat
)

func main() {

	godotenv.Load()

	config := struct {
		alertmanager     *url.URL
		boltPath         string
		consul           *url.URL
		listenAddr       string
		logLevel         string
		logJSON          bool
		store            string
		telegramAdmins   []int
		telegramToken    string
		telegramChats    []int64
		telegramVerbose  bool
		templatesPaths   []string
		translationsPath string
	}{}

	a := kingpin.New("alertmanager-bot", "Bot for Prometheus' Alertmanager")
	a.HelpFlag.Short('h')

	a.Flag("alertmanager.url", "The URL that's used to connect to the alertmanager").
		Envar("ALERTMANAGER_URL").
		Default("http://localhost:9093/").
		URLVar(&config.alertmanager)

	a.Flag("bolt.path", "The path to the file where bolt persists its data").
		Envar("BOLT_PATH").
		Default("/tmp/bot.db").
		StringVar(&config.boltPath)

	a.Flag("consul.url", "The URL that's used to connect to the consul store").
		Envar("CONSUL_URL").
		Default("localhost:8500").
		URLVar(&config.consul)

	a.Flag("listen.addr", "The address the alertmanager-bot listens on for incoming webhooks").
		Envar("LISTEN_ADDR").
		Default("0.0.0.0:8080").
		StringVar(&config.listenAddr)

	a.Flag("log.json", "Tell the application to log json and not key value pairs").
		Envar("LOG_JSON").
		BoolVar(&config.logJSON)

	a.Flag("log.level", "The log level to use for filtering logs").
		Envar("LOG_LEVEL").
		Default(levelInfo).
		EnumVar(&config.logLevel, levelError, levelWarn, levelInfo, levelDebug)

	a.Flag("store", "The store to use").
		Required().
		Envar("STORE").
		EnumVar(&config.store, storeBolt, storeConsul)

	a.Flag("telegram.admin", "The ID of the initial Telegram Admin").
		Required().
		Envar("TELEGRAM_ADMIN").
		IntsVar(&config.telegramAdmins)

	a.Flag("telegram.token", "The token used to connect with Telegram").
		Required().
		Envar("TELEGRAM_TOKEN").
		StringVar(&config.telegramToken)

	a.Flag("telegram.chat", "The ID of the initial (optional) Telegram Chat").
		Envar("TELEGRAM_CHAT").
		Default("0x7FFFFFFFFFFFFFFF").
		Int64ListVar(&config.telegramChats)

	a.Flag("telegram.verbose", "Set Telegram library to verbose mode (for debugging purpose)").
		Envar("TELEGRAM_VERBOSE").
		Default("false").
		BoolVar(&config.telegramVerbose)

	a.Flag("template.paths", "The paths to the template").
		Envar("TEMPLATE_PATHS").
		Default("/templates/default.tmpl").
		ExistingFilesVar(&config.templatesPaths)

	a.Flag("translations.path", "The path to the translations YAML").
		Envar("TRANSLATIONS_PATH").
		Default("/dicts").
		StringVar(&config.translationsPath)

	_, err := a.Parse(os.Args[1:])
	if err != nil {
		fmt.Printf("error parsing commandline arguments: %v\n", err)
		a.Usage(os.Args[1:])
		os.Exit(2)
	}

	levelFilter := map[string]level.Option{
		levelError: level.AllowError(),
		levelWarn:  level.AllowWarn(),
		levelInfo:  level.AllowInfo(),
		levelDebug: level.AllowDebug(),
	}

	logger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	if config.logJSON {
		logger = log.NewJSONLogger(log.NewSyncWriter(os.Stderr))
	}

	logger = level.NewFilter(logger, levelFilter[config.logLevel])
	logger = log.With(logger,
		"ts", log.DefaultTimestampUTC,
		"caller", log.DefaultCaller,
	)
	tlogger := log.With(logger, "component", "telegram")
	wlogger := log.With(logger, "component", "webserver")

	//----------------------------------------------------------------------------
	// Localization init
	//----------------------------------------------------------------------------
	dict, err := translation.ParseYAMLDict(config.translationsPath, logger)
	if err != nil {
		panic(err)
	}

	fallback := language.MustParse("en")
	cat, err := catalog.NewFromMap(dict, catalog.Fallback(fallback))
	if err != nil {
		panic(err)
	}

	translator := message.NewPrinter(cat.Languages()[0], message.Catalog(cat))

	//----------------------------------------------------------------------------
	// Template init
	//----------------------------------------------------------------------------
	var tmpl *vendor.Template
	funcs := vendor.DefaultFuncs
	funcs["since"] = func(t time.Time) string {
		return durafmt.Parse(time.Since(t)).String()
	}
	funcs["duration"] = func(start time.Time, end time.Time) string {
		return durafmt.Parse(end.Sub(start)).String()
	}

	vendor.DefaultFuncs = funcs

	tmpl, err = vendor.FromGlobs(config.templatesPaths...)
	if err != nil {
		level.Error(logger).Log("msg", "failed to parse templates", "err", err)
		os.Exit(1)
	}
	tmpl.ExternalURL = config.alertmanager

	//----------------------------------------------------------------------------
	// Store init
	//----------------------------------------------------------------------------
	var kvStore store.Store
	switch strings.ToLower(config.store) {

	case storeBolt:
		kvStore, err = boltdb.New([]string{config.boltPath}, &store.Config{Bucket: "alertmanager"})
		if err != nil {
			level.Error(logger).Log("msg", "failed to create bolt store backend", "err", err)
			os.Exit(1)
		}

	case storeConsul:
		kvStore, err = consul.New([]string{config.consul.String()}, nil)
		if err != nil {
			level.Error(logger).Log("msg", "failed to create consul store backend", "err", err)
			os.Exit(1)
		}

	default:
		level.Error(logger).Log("msg", "please provide one of the following supported store backends: bolt, consul")
		os.Exit(1)
	}
	defer kvStore.Close()

	//----------------------------------------------------------------------------
	// Chats subscribtion init (if not default value)
	//----------------------------------------------------------------------------
	if config.telegramChats[0] != int64(0x7FFFFFFFFFFFFFFF) {

		chats = make([]telebot.Chat, len(config.telegramChats))
		for each, id := range config.telegramChats {
			chats[each].ID = id
		}

	}

	// TODO Needs fan out for multiple bots
	webhooks := make(chan vendor.Message, 32)
	quit := make(chan bool)

	//////////////////////////////////////////////////////////////////////////////
	// MAIN GOROUTINES
	//////////////////////////////////////////////////////////////////////////////

	//----------------------------------------------------------------------------
	// alertbot start and serving webhooks goroutine
	//----------------------------------------------------------------------------
	chatStore, err := telegram.NewChatStore(kvStore)
	if err != nil {
		level.Error(tlogger).Log("msg", "failed to create chat store", "err", err)
		os.Exit(1)
	}

	bot, err := telegram.NewBot(
		chatStore, config.telegramToken, config.telegramAdmins[0], config.telegramVerbose,
		telegram.WithLogger(logger),
		telegram.WithAddr(config.listenAddr),
		telegram.WithAlertmanager(config.alertmanager),
		telegram.WithTranslation(translator),
		telegram.WithTemplates(tmpl),
		telegram.WithRevision(Revision),
		telegram.WithStartTime(StartTime),
		telegram.WithExtraAdmins(config.telegramAdmins[1:]...),
		telegram.WithChatsToSubscribe(chats...),
	)
	if err != nil {
		level.Error(tlogger).Log("msg", "failed to create bot", "err", err)
		os.Exit(2)
	}

	level.Info(tlogger).Log(
		"msg", "starting alertmanager-bot",
		"version", Version,
		"revision", Revision,
		"buildDate", BuildDate,
		"goVersion", GoVersion,
	)

	level.Debug(tlogger).Log(
		"msg", "with this environment",
		"alertmanager_url", config.alertmanager,
		"log_level", config.logLevel,
		"admins", fmt.Sprint(config.telegramAdmins),
		"store", config.store,
		"lang", fmt.Sprint(cat.Languages()),
	)

	// Serve Alertmanager webhooks
	level.Info(tlogger).Log("msg", "starting webhooks serving")
	go bot.Serve(webhooks)

	go func() {
		for {
			select {
			case <-quit:
				return
			default:
				bot.Handle(telebot.OnText, func(message *telebot.Message) {
					bot.HandleCommands(message)
				})

				// Start communicating with Telegram
				bot.Start()
				defer bot.Stop()
			}
		}
	}()

	//----------------------------------------------------------------------------
	// Webserver goroutine
	//----------------------------------------------------------------------------
	// TODO: Use Heptio's healthcheck library
	handleHealth := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}

	webhooksCounter := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alertmanagerbot",
		Name:      "webhooks_total",
		Help:      "Number of webhooks received by this bot",
	})

	prometheus.MustRegister(webhooksCounter)

	mux := http.NewServeMux()

	mux.HandleFunc("/", alertmanager.HandleWebhook(wlogger, webhooksCounter, webhooks))

	mux.Handle("/metrics", promhttp.Handler())

	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/healthy", handleHealth)
	mux.HandleFunc("/healthz", handleHealth)

	level.Info(wlogger).Log("listen_address", config.listenAddr)
	listener, err := net.Listen("tcp", config.listenAddr)
	if err != nil {
		level.Error(wlogger).Log("err", err.Error())
		os.Exit(1)
	}

	go closeListenerOnQuit(listener, quit, wlogger)

	err = (&http.Server{Addr: config.listenAddr, Handler: mux}).Serve(listener)
	if err != nil {
		level.Error(wlogger).Log("msg", "HTTP server stopped", "err", err.Error())
		os.Exit(1)
	}
}

// closeListenerOnQuit closes the provided listener upon closing the provided
// 'quit' or upon receiving a SIGINT or SIGTERM.
func closeListenerOnQuit(listener net.Listener, quit <-chan bool, logger log.Logger) {

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-signals:
		level.Warn(logger).Log("msg", "Received SIGINT/SIGTERM; exiting gracefully...")
		break
	case <-quit:
		level.Warn(logger).Log("msg", "Received termination request via web service, exiting gracefully...")
		break
	}

	listener.Close()

}
