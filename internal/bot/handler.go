package bot

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/format"
	"dota-doggo/internal/opendota"
	"dota-doggo/internal/telegram"
)

type Repository interface {
	EnsureTopic(context.Context, int64, *int64, *string, string) (domain.Topic, error)
	SetTimezone(context.Context, int64, string) error
	SetPaused(context.Context, int64, bool) error
	UpsertPlayer(context.Context, int64, string, *string) (int64, error)
	AddPlayer(context.Context, int64, int64, *string, *int64) (bool, error)
	RemovePlayer(context.Context, int64, string) (bool, error)
	Players(context.Context, int64) ([]domain.Player, error)
	Matches(context.Context, []int64, time.Time, time.Time) ([]domain.Match, error)
	RecentMatches(context.Context, []int64, []int64, int) ([]domain.Match, error)
	Overview(context.Context, []int64) (domain.MatchesOverview, error)
	Runtime(context.Context, int64) (domain.RuntimeStatus, error)
	LatestReports(context.Context, int64) ([]domain.ReportRun, error)
	RecordReport(context.Context, int64, domain.ReportRun) error
	Constants(context.Context) (domain.Constants, error)
}
type Telegram interface {
	SendHTML(context.Context, domain.Topic, string) ([]int64, error)
	SendParts(context.Context, domain.Topic, []string) ([]int64, error)
	IsAdmin(context.Context, int64, int64) (bool, error)
}
type OpenDota interface {
	Profile(context.Context, int64) (opendota.Profile, error)
	RateLimits(context.Context, bool) (*opendota.RateLimits, error)
}
type ResyncQueue interface {
	Enqueue(domain.Topic, []domain.Player, int) bool
}
type Handler struct {
	Repo                      Repository
	Telegram                  Telegram
	API                       OpenDota
	Jobs                      ResyncQueue
	Username, DefaultTimezone string
	AllowedUserIDs            map[int64]bool
	AdminCheck                bool
	Now                       func() time.Time
	Formatter                 format.Formatter
	PollInterval              time.Duration
}

const help = `<b>dota-doggo</b>
/help — справка
/limits — лимиты OpenDota

В группе или topic:
/players — список игроков
/status — состояние topic и история
/report &lt;day|week|month&gt; [id или alias] — текущий период
/last [1–10] [id или alias] — последние матчи
/leaders &lt;day|week|month&gt; — топ по winrate

Управление (администраторы или allowlist):
/track &lt;id или URL&gt; [alias] — добавить игрока
/untrack &lt;id или alias&gt; — убрать игрока
/set_timezone &lt;TZ&gt; — например Europe/Moscow
/pause, /resume — остановить или возобновить опрос
/resync [1–365 дней] [id или alias] — обновить историю

История и ручные отчёты доступны во время паузы.`

func (h *Handler) Handle(ctx context.Context, m telegram.Message) error {
	// A quota wait or broken dependency must not occupy polling indefinitely.
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	command, args, ok := Command(m.Text, h.Username)
	if !ok {
		return nil
	}
	known := map[string]bool{"help": true, "limits": true, "players": true, "status": true, "track": true, "untrack": true, "report": true, "last": true, "leaders": true, "set_timezone": true, "pause": true, "resume": true, "resync": true}
	if !known[command] {
		return nil
	}
	destination := domain.Topic{ChatID: m.Chat.ID, ThreadID: m.ThreadID}
	reply := func(text string) error { _, err := h.Telegram.SendHTML(ctx, destination, text); return err }
	if command == "help" {
		return reply(help)
	}
	if command == "limits" {
		return h.limits(ctx, destination)
	}
	if m.Chat.Type != "group" && m.Chat.Type != "supergroup" {
		return reply("Команда доступна только в группе или topic.")
	}
	manage := command == "track" || command == "untrack" || command == "set_timezone" || command == "pause" || command == "resume" || command == "resync"
	if manage {
		allowed := m.From != nil && m.From.ID > 0
		if allowed && h.AdminCheck && !h.AllowedUserIDs[m.From.ID] {
			var err error
			allowed, err = h.Telegram.IsAdmin(ctx, m.Chat.ID, m.From.ID)
			if err != nil {
				_ = reply("Не удалось проверить права. Попробуйте позже.")
				return err
			}
		}
		if !allowed {
			return reply("Команда доступна только админам чата или разрешенным пользователям.")
		}
	}
	zone := h.DefaultTimezone
	if zone == "" {
		zone = "UTC"
	}
	topic, err := h.Repo.EnsureTopic(ctx, m.Chat.ID, m.ThreadID, m.Chat.Title, zone)
	if err != nil {
		_ = reply("Не удалось прочитать настройки topic. Попробуйте позже.")
		return err
	}
	err = h.execute(ctx, topic, m, command, args)
	if err != nil && ctx.Err() == nil {
		_ = reply("Не удалось выполнить команду. Попробуйте позже.")
	}
	return err
}
func (h *Handler) execute(ctx context.Context, t domain.Topic, m telegram.Message, command, args string) error {
	reply := func(text string) error { _, err := h.Telegram.SendHTML(ctx, t, text); return err }
	switch command {
	case "track":
		input, alias := first(args)
		id, err := AccountID(input)
		if err != nil {
			return reply("Использование: /track &lt;account ID или URL /players/id&gt; [alias]")
		}
		apiCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		p, err := h.API.Profile(apiCtx, id)
		cancel()
		if err != nil {
			return err
		}
		pid, err := h.Repo.UpsertPlayer(ctx, p.AccountID, p.Name, p.URL)
		if err != nil {
			return err
		}
		var name *string
		if alias != "" {
			name = &alias
		}
		added, err := h.Repo.AddPlayer(ctx, t.ID, pid, name, &m.From.ID)
		if err != nil {
			return err
		}
		if !added {
			return reply("Игрок уже отслеживается в этом topic.")
		}
		label := p.Name
		if name != nil {
			label = *name
		}
		return reply(fmt.Sprintf("Добавлен: <b>%s</b> (%d). Первый опрос сохранит историю без уведомлений.", html.EscapeString(label), id))
	case "set_timezone":
		if _, err := time.LoadLocation(args); args == "" || err != nil {
			return reply("Неизвестная таймзона. Пример: Europe/Moscow")
		}
		if err := h.Repo.SetTimezone(ctx, t.ID, args); err != nil {
			return err
		}
		return reply("Таймзона: <b>" + html.EscapeString(args) + "</b>")
	case "pause", "resume":
		if err := h.Repo.SetPaused(ctx, t.ID, command == "pause"); err != nil {
			return err
		}
		if command == "pause" {
			return reply("Опрос topic приостановлен. Автоотчёты продолжаются.")
		}
		return reply("Опрос topic возобновлён.")
	}
	players, err := h.Repo.Players(ctx, t.ID)
	if err != nil {
		return err
	}
	switch command {
	case "players":
		if len(players) == 0 {
			return reply("В этом topic пока нет игроков. Добавление: /track &lt;account ID&gt; [alias]")
		}
		lines := []string{"<b>Игроки topic</b>"}
		for _, p := range players {
			lines = append(lines, fmt.Sprintf("• <b>%s</b> · %d", html.EscapeString(p.Label()), p.AccountID))
		}
		return reply(strings.Join(lines, "\n"))
	case "untrack":
		if args == "" {
			return reply("Использование: /untrack &lt;account ID или alias&gt;")
		}
		selected, message := choose(players, args)
		if message != "" {
			return reply(message)
		}
		_, err := h.Repo.RemovePlayer(ctx, t.ID, strconv.FormatInt(selected[0].AccountID, 10))
		if err != nil {
			return err
		}
		return reply("Игрок удалён из этого topic. История сохранена.")
	case "status":
		return h.status(ctx, t, players)
	case "resync":
		days, filter, message := countFilter(args, 7, 365)
		if message != "" {
			return reply("Использование: /resync [1–365 дней] [account ID или alias]")
		}
		selected, message := choose(players, filter)
		if message != "" {
			return reply(message)
		}
		if len(selected) == 0 {
			return reply("В этом topic пока нет игроков.")
		}
		if h.Jobs == nil || !h.Jobs.Enqueue(t, selected, days) {
			return reply("Задача уже выполняется для этого topic или очередь заполнена. Попробуйте позже.")
		}
		return reply(fmt.Sprintf("Обновление истории за %d дней поставлено в очередь. Результат появится в этом topic.", days))
	case "last":
		count, filter, message := countFilter(args, 5, 10)
		if message != "" {
			return reply("Использование: /last [1–10] [account ID или alias]")
		}
		selected, message := choose(players, filter)
		if message != "" {
			return reply(message)
		}
		matches, err := h.Repo.RecentMatches(ctx, ids(selected), ids(players), count)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			return reply("Матчей пока нет. Историю можно загрузить через /resync.")
		}
		constants, err := h.Repo.Constants(ctx)
		if err != nil {
			return err
		}
		// Put the selected player first without dropping the other participants.
		ordered := append([]domain.Player{}, selected...)
		for _, p := range players {
			found := false
			for _, q := range selected {
				if p.ID == q.ID {
					found = true
					break
				}
			}
			if !found {
				ordered = append(ordered, p)
			}
		}
		var sections []string
		for _, group := range format.GroupMatches(ordered, matches) {
			sections = append(sections, h.Formatter.RecentMatch(group, constants, t.Timezone))
		}
		parts, err := format.SplitSections("<b>Последние матчи</b>", sections, format.TelegramTextLimit)
		if err != nil {
			return err
		}
		_, err = h.Telegram.SendParts(ctx, t, parts)
		return err
	case "report", "leaders":
		periodText, filter := first(args)
		period := domain.Period(periodText)
		if !period.Valid() || (command == "leaders" && filter != "") {
			return reply("Укажите период: day, week или month.")
		}
		selected, message := choose(players, filter)
		if message != "" {
			return reply(message)
		}
		now := time.Now()
		if h.Now != nil {
			now = h.Now()
		}
		start, end, err := period.Bounds(now, t.Timezone, false)
		if err != nil {
			return err
		}
		matches, err := h.Repo.Matches(ctx, ids(selected), start, end)
		if err != nil {
			return err
		}
		summaries := domain.TopicSummaries(selected, period, start, end, matches, command == "leaders" || period == domain.Day)
		if len(summaries) == 0 {
			return reply("Для выбранного периода данных нет.")
		}
		if command == "leaders" {
			return reply(h.Formatter.Leaderboard("Лидеры · "+periodText, domain.Leaders(summaries, 10)))
		}
		constants, err := h.Repo.Constants(ctx)
		if err != nil {
			return err
		}
		parts, err := format.SplitSections("<b>Отчёт · "+periodText+"</b>", h.Formatter.ReportSections(summaries, constants), format.TelegramTextLimit)
		if err != nil {
			return err
		}
		messages, err := h.Telegram.SendParts(ctx, t, parts)
		if err != nil {
			return err
		}
		var messageID *int64
		if len(messages) > 0 {
			messageID = &messages[len(messages)-1]
		}
		return h.Repo.RecordReport(ctx, t.ID, domain.ReportRun{Period: periodText, Trigger: "manual", Start: start, End: end, MessageID: messageID})
	}
	return nil
}
func countFilter(args string, fallback, maximum int) (int, string, string) {
	head, tail := first(args)
	if head == "" {
		return fallback, "", ""
	}
	n, err := strconv.Atoi(head)
	if err != nil {
		return fallback, args, ""
	}
	if n < 1 || n > maximum {
		return 0, "", "out of range"
	}
	return n, tail, ""
}
func (h *Handler) limits(ctx context.Context, t domain.Topic) error {
	apiCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	limits, err := h.API.RateLimits(apiCtx, true)
	cancel()
	if err != nil {
		_, _ = h.Telegram.SendHTML(ctx, t, "Не удалось обновить лимиты OpenDota. Попробуйте позже.")
		return err
	}
	text := "OpenDota не передал заголовки лимитов."
	if limits != nil {
		show := func(v *int64) string {
			if v == nil {
				return "неизвестно"
			}
			return strconv.FormatInt(*v, 10)
		}
		text = fmt.Sprintf("<b>OpenDota</b>\nМинута: %s / %s\nДень: %s / %s\nПауза между запросами: %s", show(limits.RemainingMinute), show(limits.LimitMinute), show(limits.RemainingDay), show(limits.LimitDay), limits.RecommendedPause.Round(time.Second))
		if limits.ServerTime != nil {
			text += "\nВремя сервера: " + format.Datetime(*limits.ServerTime, "UTC")
		}
	}
	_, err = h.Telegram.SendHTML(ctx, t, text)
	return err
}
func (h *Handler) status(ctx context.Context, t domain.Topic, players []domain.Player) error {
	overview, err := h.Repo.Overview(ctx, ids(players))
	if err != nil {
		return err
	}
	// A topic that has never been polled has no runtime row yet.
	runtime, err := h.Repo.Runtime(ctx, t.ID)
	if err != nil {
		return err
	}
	reports, err := h.Repo.LatestReports(ctx, t.ID)
	if err != nil {
		return err
	}
	thread := "основной чат"
	if t.ThreadID != nil {
		thread = strconv.FormatInt(*t.ThreadID, 10)
	}
	lines := []string{fmt.Sprintf("<b>Topic status</b>\nChat: %d · Thread: %s\nTimezone: %s · Paused: %t\nИгроков: %d · Записей: %d · Уникальных матчей: %d", t.ChatID, thread, html.EscapeString(t.Timezone), t.Paused, len(players), overview.TotalRows, overview.UniqueMatches)}
	stamp := func(at *time.Time) string {
		if at == nil {
			return "ещё не было"
		}
		return format.Datetime(*at, t.Timezone)
	}
	lines = append(lines, "Последний матч: "+stamp(overview.LastEnd), "Опрос начат: "+stamp(runtime.StartedAt), "Опрос завершён: "+stamp(runtime.FinishedAt), "Успешный опрос: "+stamp(runtime.SucceededAt))
	if t.Title != nil {
		lines = append(lines, "Название: "+html.EscapeString(*t.Title))
	}
	if !t.CreatedAt.IsZero() {
		lines = append(lines, "Создан: "+format.Datetime(t.CreatedAt, t.Timezone))
	}
	interval := h.PollInterval
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	lines = append(lines, "Интервал опроса: "+interval.String())
	inFlight := runtime.StartedAt != nil && (runtime.FinishedAt == nil || runtime.FinishedAt.Before(*runtime.StartedAt))
	switch {
	case t.Paused:
		lines = append(lines, "Следующий опрос: на паузе")
	case inFlight:
		lines = append(lines, "Опрос выполняется")
	case runtime.FinishedAt != nil:
		// The serial worker waits after the entire cycle. This topic's finish
		// is a lower bound, not a promise of an exact schedule.
		lines = append(lines, "Следующий опрос: не раньше "+format.Datetime(runtime.FinishedAt.Add(interval), t.Timezone))
	default:
		lines = append(lines, "Следующий опрос: первый цикл worker")
	}
	if runtime.StartedAt != nil && runtime.FinishedAt != nil && !inFlight {
		lines = append(lines, "Длительность опроса: "+runtime.FinishedAt.Sub(*runtime.StartedAt).Round(time.Second).String())
	}
	if runtime.Error != nil {
		lines = append(lines, "Ошибка: "+html.EscapeString(*runtime.Error))
	}
	for _, p := range players {
		cursor := "не инициализирован"
		if p.LastSeenMatchID != nil {
			cursor = strconv.FormatInt(*p.LastSeenMatchID, 10)
		}
		lines = append(lines, fmt.Sprintf("%s (%d): cursor %s", html.EscapeString(p.Label()), p.AccountID, cursor))
	}
	for _, r := range reports {
		lines = append(lines, fmt.Sprintf("Отчёт %s (%s): %s · %s — %s", html.EscapeString(r.Period), html.EscapeString(r.Trigger), format.Datetime(r.CreatedAt, t.Timezone), format.Datetime(r.Start, t.Timezone), format.Datetime(r.End, t.Timezone)))
	}
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	for _, period := range []domain.Period{domain.Day, domain.Week, domain.Month} {
		start, end, err := period.Bounds(now, t.Timezone, true)
		if err != nil {
			return err
		}
		lines = append(lines, fmt.Sprintf("Период автоотчёта %s: %s — %s", period, format.Datetime(start, t.Timezone), format.Datetime(end, t.Timezone)))
	}
	_, err = h.Telegram.SendHTML(ctx, t, strings.Join(lines, "\n"))
	return err
}
