package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/pyed/transmission"
)

const turtleUsage = "<b>turtle:</b> give on, off, <code>schedule HH:MM-HH:MM [days]</code>, " +
	"<code>schedule off</code>, or <code>limits DOWN UP</code> in KB/s"

// Transmission's schedule days: a bitmask from Sunday, 1, to Saturday, 64.
const (
	everyDay = 127
	weekdays = 62
	weekends = 65
)

var dayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// turtle switches turtle mode, Transmission's alternative speed limits: on,
// off, or the other way round without an argument. With schedule, it shows
// or sets when Transmission switches it by itself; with limits, it sets its
// limits.
func (b *Bot) turtle(ctx context.Context, r *request) {
	if len(r.args) > 0 {
		switch strings.ToLower(r.args[0]) {
		case "schedule":
			b.turtleSchedule(ctx, r, r.args[1:])
			return
		case "limits", "limit":
			b.turtleLimits(ctx, r, r.args[1:])
			return
		}
	}
	var requested *bool
	if len(r.args) > 1 {
		b.reply(ctx, r, turtleUsage)
		return
	}
	if len(r.args) == 1 {
		var enabled bool
		switch strings.ToLower(r.args[0]) {
		case "on", "1", "true", "enable":
			enabled = true
		case "off", "0", "false", "disable":
			enabled = false
		default:
			b.reply(ctx, r, turtleUsage)
			return
		}
		requested = &enabled
	}

	sess, err := b.Client.GetSession(ctx)
	if err != nil {
		b.replyError(ctx, r, "turtle", err)
		return
	}

	enabled := !sess.AltSpeedEnabled
	if requested != nil {
		enabled = *requested
	}

	if err := b.Client.SetAltSpeedEnabled(ctx, enabled); err != nil {
		b.replyError(ctx, r, "turtle", err)
		return
	}

	if enabled {
		b.reply(ctx, r, fmt.Sprintf("🐢 <b>Turtle mode enabled</b> (limits: ↓ %d KB/s  ↑ %d KB/s)", sess.AltSpeedDown, sess.AltSpeedUp))
	} else {
		b.reply(ctx, r, "🐇 <b>Turtle mode disabled</b>")
	}
}

// turtleSchedule shows or sets turtle mode's schedule: HH:MM-HH:MM, which
// may cross midnight, on the days given (every day, weekdays, weekends, or
// day names such as sat,sun), or off.
func (b *Bot) turtleSchedule(ctx context.Context, r *request, args []string) {
	if len(args) > 0 {
		var options transmission.SessionSetOptions
		if len(args) == 1 && strings.EqualFold(args[0], "off") {
			off := false
			options.AltSpeedTimeEnabled = &off
		} else {
			begin, end, ok := parseSpan(args[0])
			days, daysOK := parseDays(strings.ToLower(strings.Join(args[1:], " ")))
			if !ok || !daysOK {
				b.reply(ctx, r, turtleUsage)
				return
			}
			on := true
			options.AltSpeedTimeEnabled, options.AltSpeedTimeBegin, options.AltSpeedTimeEnd, options.AltSpeedTimeDay = &on, &begin, &end, &days
		}
		if err := b.Client.SetSession(ctx, options); err != nil {
			b.replyError(ctx, r, "turtle", err)
			return
		}
	}
	session, err := b.Client.GetSession(ctx)
	if err != nil {
		b.replyError(ctx, r, "turtle", err)
		return
	}
	if !session.AltSpeedTimeEnabled {
		b.reply(ctx, r, "🐢 <b>Turtle schedule:</b> off")
		return
	}
	b.reply(ctx, r, fmt.Sprintf("🐢 <b>Turtle schedule:</b> %s–%s, %s (↓ %d KB/s ↑ %d KB/s)",
		clock(session.AltSpeedTimeBegin), clock(session.AltSpeedTimeEnd), describeDays(session.AltSpeedTimeDay),
		session.AltSpeedDown, session.AltSpeedUp))
}

// turtleLimits sets turtle mode's limits, in KB/s.
func (b *Bot) turtleLimits(ctx context.Context, r *request, args []string) {
	if len(args) != 2 {
		b.reply(ctx, r, turtleUsage)
		return
	}
	down, errDown := strconv.ParseUint(args[0], 10, 31)
	up, errUp := strconv.ParseUint(args[1], 10, 31)
	if errDown != nil || errUp != nil {
		b.reply(ctx, r, turtleUsage)
		return
	}
	downLimit, upLimit := int64(down), int64(up)
	if err := b.Client.SetSession(ctx, transmission.SessionSetOptions{AltSpeedDown: &downLimit, AltSpeedUp: &upLimit}); err != nil {
		b.replyError(ctx, r, "turtle", err)
		return
	}
	b.reply(ctx, r, fmt.Sprintf("🐢 <b>Turtle limits:</b> ↓ %d KB/s ↑ %d KB/s", downLimit, upLimit))
}

// parseSpan reads HH:MM-HH:MM into minutes after midnight.
func parseSpan(span string) (begin, end int, ok bool) {
	from, to, found := strings.Cut(span, "-")
	if !found {
		return 0, 0, false
	}
	begin, okBegin := parseClock(from)
	end, okEnd := parseClock(to)
	return begin, end, okBegin && okEnd && begin != end
}

func parseClock(text string) (int, bool) {
	hours, minutes, found := strings.Cut(text, ":")
	h, errH := strconv.Atoi(hours)
	m, errM := strconv.Atoi(minutes)
	if !found || errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// clock writes minutes after midnight as HH:MM.
func clock(minutes int) string {
	return fmt.Sprintf("%02d:%02d", minutes/60%24, minutes%60)
}

// parseDays reads the days a schedule applies on into Transmission's mask.
func parseDays(text string) (int, bool) {
	switch text {
	case "", "every day", "everyday", "daily", "all":
		return everyDay, true
	case "weekdays":
		return weekdays, true
	case "weekends":
		return weekends, true
	}
	mask := 0
	for _, name := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' }) {
		day := -1
		for i, known := range dayNames {
			if len(name) >= 3 && strings.HasPrefix(name, known) {
				day = i
			}
		}
		if day < 0 {
			return 0, false
		}
		mask |= 1 << day
	}
	return mask, mask != 0
}

// describeDays names the days of a schedule mask.
func describeDays(mask int) string {
	switch mask & everyDay {
	case everyDay:
		return "every day"
	case weekdays:
		return "weekdays"
	case weekends:
		return "weekends"
	}
	var days []string
	for i, name := range dayNames {
		if mask&(1<<i) != 0 {
			days = append(days, strings.ToUpper(name[:1])+name[1:])
		}
	}
	return strings.Join(days, ", ")
}
