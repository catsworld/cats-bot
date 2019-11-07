package botmaid

import (
	"fmt"
	"sort"
	"strings"

	"github.com/catsworld/botmaid/random"
)

// HelpMenu describes the menu item of the help.
type HelpMenu struct {
	Menu, Help string
	Names      []string
}

func (bm *BotMaid) pushHelp(hc string, u *Update, showUndef bool) {
	dontknow := true

	for _, v := range bm.HelpMenus {
		if hc == v.Menu || In(hc, v.Names) {
			s := ""

			for _, v := range bm.Commands {
				if v.Master && !bm.IsMaster(u.User) {
					continue
				}
				if v.Menu == hc {
					dontknow = false
					if v.Help != "" {
						s += v.Names[0] + v.Help + "\n"
					}
				}
			}

			if len(s) > 0 && s[len(s)-1] == '\n' {
				s = s[:len(s)-1]
			}

			s = At(u.User) + "\n" + s
			Reply(u, s)
			return
		}
	}

	s := ""

	for _, c := range bm.Commands {
		if c.Master && !bm.IsMaster(u.User) {
			continue
		}
		for _, n := range c.Names {
			if n == hc {
				dontknow = false
				if c.Help != "" {
					s += n + c.Help + "\n"
				}
				break
			}
		}
	}

	if s != "" {
		dontknow = false
	}

	if !dontknow {
		s = strings.TrimSpace(s)

		if s != "" {
			s = At(u.User) + "\n" + s
			Reply(u, s)
			return
		}

		Reply(u, fmt.Sprintf(random.String(bm.Words["noHelpText"])), At(u.User), u.Message.Command)
		return
	}

	if !showUndef {
		return
	}

	Reply(u, fmt.Sprintf(random.String(bm.Words["undefCommand"]), At(u.User), hc))
}

func (bm *BotMaid) help(u *Update) bool {
	if IsCommand(u, "help") && len(u.Message.Flag.Args()) == 1 {
		s := fmt.Sprintf(random.String(bm.Words["selfIntro"]), u.User.NickName) + "\n\n"

		menus := []string{}

		for _, v := range bm.HelpMenus {
			menus = append(menus, v.Menu)
		}

		sort.Strings(menus)

		for _, k := range menus {
			f := false

			for _, c := range bm.Commands {
				if c.Master && !bm.IsMaster(u.User) {
					continue
				}
				if c.Menu == k {
					f = true
					break
				}
			}

			if f {
				for _, v := range bm.HelpMenus {
					if k == v.Menu || In(k, v.Names) {
						s += k + " - " + v.Help + "\n"
						break
					}
				}
			}
		}

		if len(s) > 0 && s[len(s)-1] == '\n' {
			s = s[:len(s)-1]
		}

		s = At(u.User) + "\n" + s
		Reply(u, s)
		return true
	}

	hc := ""
	if IsCommand(u, "help") && len(u.Message.Flag.Args()) == 2 {
		hc = u.Message.Flag.Args()[1]
	} else if IsCommand(u) && len(u.Message.Flag.Args()) == 2 && In(u.Message.Flag.Args()[1], "help") {
		hc = bm.extractCommand(u)
	} else {
		return false
	}

	bm.pushHelp(hc, u, true)
	return true
}

func (bm *BotMaid) help2(u *Update) bool {
	if IsCommand(u) {
		for _, c := range bm.Commands {
			if len(c.Names) != 0 && !IsCommand(u, c.Names) {
				continue
			}
			if c.ArgsMinLen != 0 && len(u.Message.Flag.Args()) < c.ArgsMinLen {
				continue
			}
			if c.ArgsMaxLen != 0 && len(u.Message.Flag.Args()) > c.ArgsMaxLen {
				continue
			}

			if !bm.IsMaster(u.User) && c.Master {
				Reply(u, fmt.Sprintf(random.String(bm.Words["noPermission"]), At(u.User), u.Message.Command))
				return true
			}
		}

		hc := u.Message.Command
		bm.pushHelp(hc, u, false)
		return true
	}

	return false
}
