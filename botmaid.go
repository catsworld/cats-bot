// Package botmaid is a package for managing bots.
package botmaid

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis"
	"github.com/google/shlex"
	"github.com/pelletier/go-toml"
	"github.com/spf13/pflag"
)

type botmaidRedisConfig struct {
	Address  string
	Password string
	Database int
}

type botMaidConfig struct {
	Redis         botmaidRedisConfig
	Log           bool
	CommandPrefix []string
}

// BotMaid includes a slice of Bot and some methods to use them.
type BotMaid struct {
	Bots map[string]*Bot

	Conf *botMaidConfig

	Redis *redis.Client

	Commands CommandSlice
	Timers   []*Timer
	Helps    []*Help

	Words      map[string]string
	SubEntries []string

	respTime time.Time
	history  map[int64][]time.Time
}

func (bm *BotMaid) readBotConfig(conf *toml.Tree, section string) error {
	botType := conf.Get(section + ".Type").(string)

	b := &Bot{
		ID:      section,
		API:     new(API),
		BotMaid: bm,
	}

	if botType == "QQ" {
		q := &APICqhttp{}

		if s, ok := conf.Get(section + ".AccessToken").(string); ok {
			q.AccessToken = s
		}
		if s, ok := conf.Get(section + ".Secret").(string); ok {
			q.Secret = s
		}
		if s, ok := conf.Get(section + ".APIEndpoint").(string); ok {
			q.APIEndpoint = s
		}
		if s, ok := conf.Get(section + ".WebsocketEndpoint").(string); ok {
			q.WebsocketEndpoint = s
		}

		for {
			m, err := q.API("get_login_info", map[string]interface{}{})
			if err != nil {
				if bm.Conf.Log {
					log.Printf("Init botmaid: %v, retrying...\n", err)
				}
				time.Sleep(time.Second * 3)
				continue
			}

			u := m.(map[string]interface{})
			b.Self = &User{
				ID:       int64(u["user_id"].(float64)),
				UserName: strconv.FormatInt(int64(u["user_id"].(float64)), 10),
				NickName: u["nickname"].(string),
				Update: &Update{
					Bot: b,
				},
			}

			break
		}

		*b.API = q
	} else if botType == "Telegram" {
		t := &APITelegramBot{}

		if s, ok := conf.Get(section + ".Token").(string); ok {
			t.Token = s
		}

		for {
			m, err := t.API("getMe", map[string]interface{}{})
			if err != nil {
				if bm.Conf.Log {
					log.Printf("Init botmaid: %v, retrying...\n", err)
				}
				time.Sleep(time.Second * 3)
				continue
			}

			u := m.(map[string]interface{})
			b.Self = &User{
				ID:       int64(u["id"].(float64)),
				NickName: u["first_name"].(string),
				Update: &Update{
					Bot: b,
				},
			}
			if u["last_name"] != nil {
				b.Self.NickName += " " + u["last_name"].(string)
			}
			if u["username"] != nil {
				b.Self.UserName = u["username"].(string)
			}

			break
		}
		*b.API = t
	} else {
		return fmt.Errorf("Init botmaid: Unknown type of %v", section)
	}

	if ms, ok := conf.Get(section + ".Master").([]interface{}); ok {
		for _, v := range ms {
			if id, ok := v.(int64); ok {
				bm.Redis.SAdd("master_"+b.ID, id)
			}
		}
	}

	bm.Bots[section] = b
	return nil
}

func (bm *BotMaid) startBot() {
	for _, b := range bm.Bots {
		bot := b
		go func(b *Bot) {
			updates, errors := (*b.API).Pull(&PullConfig{
				Limit:            100,
				Timeout:          60,
				RetryWaitingTime: time.Second * 3,
			})

			if bm.Conf.Log {
				go func() {
					for err := range errors {
						log.Printf("Bot running: %v.\n", err)
					}
				}()
				log.Printf("[%v] %v (%v) has been loaded. Begin to get updates.\n", b.ID, b.Self.NickName, (*b.API).Platform())
			}

			for u := range updates {
				up := u
				go func(u *Update) {
					if u.Message == nil || !u.Time.After(bm.respTime) {
						return
					}

					u.Bot = b

					u.Message.Flags = map[string]*pflag.FlagSet{}

					if (*b.API).Platform() == "Telegram" {
						if u.User != nil && u.User.UserName != "" {
							bm.Redis.HSet("telegramUsers", fmt.Sprintf("%v", u.User.UserName), u.User.ID)
						}

						u.Message.Content = strings.ReplaceAll(u.Message.Content, "—", "--")
					}

					if bm.Conf.Log {
						logText := u.Message.Content
						if u.User != nil {
							logText = u.User.NickName + ": " + logText
						}
						if u.Chat != nil && u.Chat.Title != "" {
							logText = "[" + u.Chat.Title + "]" + logText
						}
						log.Println(logText)
					}

					args, err := shlex.Split(u.Message.Content)
					u.Message.Args = args
					u.Message.Command = bm.extractCommand(u)
					if err != nil && u.Message.Command != "" {
						bm.Reply(u, fmt.Sprintf(bm.Words["invalidParameters"], bm.At(u.User), u.Message.Content))
						return
					}

					for _, c := range bm.Commands {
						if c.Help != nil && c.Help.Menu != "" {
							if c.Help.SetFlag == nil {
								c.Help.SetFlag = func(flag *pflag.FlagSet) {}
							}

							u.Message.Flags[c.Help.Menu] = pflag.NewFlagSet(c.Help.Menu, pflag.ContinueOnError)
							u.Message.Flags[c.Help.Menu].SortFlags = true
							c.Help.SetFlag(u.Message.Flags[c.Help.Menu])

							u.Message.Flags[c.Help.Menu].Parse(u.Message.Args)
						}
					}

					for _, c := range bm.Commands {
						if c.Help != nil && len(c.Help.Names) != 0 && !Contains(c.Help.Names, u.Message.Command) {
							continue
						}

						if c.Help == nil || c.Help.Menu == "" {
							if c.Do(u, nil) {
								break
							}
							continue
						}

						if c.Do(u, u.Message.Flags[c.Help.Menu]) {
							break
						}
					}
				}(up)
			}
		}(bot)
	}
}

// New creates a BotMaid.
func New(configFile string) (*BotMaid, error) {
	bm := &BotMaid{
		Bots: map[string]*Bot{},
		Conf: &botMaidConfig{
			Log: true,
		},

		respTime: time.Now(),
		history:  map[int64][]time.Time{},
	}

	conf, err := toml.LoadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("Init botmaid: Read config: %v", err)
	}

	if f, ok := conf.Get("Log.Log").(bool); ok {
		bm.Conf.Log = f
	}

	if ss, ok := conf.Get("Command.Prefix").([]interface{}); ok {
		for _, v := range ss {
			if s, ok := v.(string); ok {
				bm.Conf.CommandPrefix = append(bm.Conf.CommandPrefix, s)
			}
		}
	} else {
		bm.Conf.CommandPrefix = []string{"/"}
	}

	if conf.Has("Redis") {
		bm.Conf.Redis.Address = "127.0.0.1"
		if s, ok := conf.Get("Redis.Address").(string); ok {
			bm.Conf.Redis.Address = s
		}
		if s, ok := conf.Get("Redis.Password").(string); ok {
			bm.Conf.Redis.Password = s
		}
		if a, ok := conf.Get("Redis.Database").(int64); ok {
			bm.Conf.Redis.Database = int(a)
		}
	}

	if bm.Conf.Redis.Address != "" {
		bm.Redis = redis.NewClient(&redis.Options{
			Addr:     bm.Conf.Redis.Address,
			Password: bm.Conf.Redis.Password,
			DB:       bm.Conf.Redis.Database,
		})
	}

	for _, v := range conf.Keys() {
		if strings.HasPrefix(v, "Bot_") {
			if conf.Get(v) == nil {
				break
			}

			if conf.Get(v+".Type") == nil {
				return nil, fmt.Errorf("Init botmaid: Missing type of %v", v)
			}

			err := bm.readBotConfig(conf, v)
			if err != nil {
				return nil, fmt.Errorf("Read config: %v", err)
			}
		}
	}

	bm.Words = map[string]string{
		"selfIntro": fmt.Sprintf(`%%v is a bot.

Usage:

%v(%v)*COMMAND* [ARGUMENTS]

The commands are:
%%v

Use "help [COMMAND] for more information about a command."`, bm.Conf.CommandPrefix[0], ListToString(bm.Conf.CommandPrefix[1:], "%v", ", ", " or ")),
		"undefCommand":        "%v, the command \"%v\" is unknown, please retry after checking the spelling or the \"help\" command.",
		"unregMaster":         "%v, the master %v has been unregistered.",
		"regMaster":           "%v, the user %v has been registered as master.",
		"noPermission":        "%v, you don't have permission to use \"%v\"",
		"invalidParameters":   "%v, the parameters of the command \"%v\" is invalid.",
		"noHelpText":          "%v, the command \"%v\" has no help text.",
		"invalidUser":         "%v, the user \"%v\" is invalid or not exist.",
		"fmtVersion":          "Version: %v",
		"fmtLog":              "%v:\n\nChangeLog:%v",
		"versionSet":          "The version has been set to %v.",
		"logAdded":            "The ChangeLog \"%v\" has been added.",
		"versionLogHelp":      "show the change log of the current version",
		"versetVerHelp":       "appoint the version to manage",
		"versetLogHelp":       "add a sentence to the change log",
		"versetBroadcastHelp": "broadcast the change log",
		"upgraded":            "New version! ",
		"subscribed":          "\"%v\" has been subscibed on this Chat.",
		"unsubscribed":        "\"%v\" has been unsubscibed on this Chat.",
		"correctSubEntries":   "These entries can be subscibed: %v",
		"subEntriesFormat":    "\"%v\"",
		"subEntriesSeparator": ", ",
		"subEntriesAnd":       " and ",
	}

	return bm, nil
}

// Start starts the BotMaid.
func (bm *BotMaid) Start() error {
	err := bm.Redis.Ping().Err()
	if err != nil {
		return fmt.Errorf("Init botmaid: Connect Redis: %v", err)
	}

	sort.Stable(CommandSlice(bm.Commands))

	bm.startBot()
	bm.loadTimers()

	select {}
}
