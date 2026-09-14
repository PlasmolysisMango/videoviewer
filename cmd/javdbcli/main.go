// Command javdbcli exercises the javdb package against the real endpoints.
//
// It is deliberately small: every sub-command maps to one Client call so the
// output doubles as documentation of what the package returns.
//
//	javdbcli search ssis-001 -sub
//	javdbcli ranking playback -period weekly
//	javdbcli ranking top250 -year 2025
//	javdbcli movie MIDA-783 -magnets
//	javdbcli magnets ssis-001
//	javdbcli browse -code SSIS -category censored
//	javdbcli actor 楓花戀 -movies
//	javdbcli tags -category censored
//	javdbcli reviews yxY7kW
//	javdbcli login -user me@example.com -pass-file -   # mint an app JWT
//
// Add -json to dump the decoded structures instead of the human table.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"videoviewer/pkg/javdb"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	var err error
	switch cmd := os.Args[1]; cmd {
	case "search":
		err = runSearch(os.Args[2:])
	case "ranking":
		err = runRanking(os.Args[2:])
	case "movie":
		err = runMovie(os.Args[2:])
	case "magnets":
		err = runMagnets(os.Args[2:])
	case "browse":
		err = runBrowse(os.Args[2:])
	case "actor":
		err = runActor(os.Args[2:])
	case "tags":
		err = runTags(os.Args[2:])
	case "reviews":
		err = runReviews(os.Args[2:])
	case "login":
		err = runLogin(os.Args[2:])
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "javdbcli: unknown command %q\n\n", cmd)
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "javdbcli: %v\n", explain(err))
		os.Exit(exitCode(err))
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `javdbcli - JavDB search / rankings / details

Usage:
  javdbcli <command> [flags] [args]

Commands:
  search     <keyword>          search titles (or -scope actor)
  ranking    playback|movies|top250|actors|fanza
  movie      <id|code>          full metadata, -magnets for the torrent table
  magnets    <id|code>          best torrent first (subtitled, then largest)
  browse     -category|-code|-maker|-series|-publisher|-director|-actor|-tag
  actor      <name|id>          profile, -movies for the filmography
  tags       -category censored  available filter tags and their ids
  reviews    <id|code>          user comments
  login      -user a@b          app JWT for TOP250 (-pass-file - reads stdin)

Flags may appear before or after the positional arguments.

Connection flags (all commands):
  -site value       web mirror, repeatable with -site a -site b
                    (default: `+strings.Join(javdb.DefaultSites, ", ")+`)
  -api value        mobile JSON API root, "" disables that backend
                    (default: `+javdb.DefaultAPIBase+`)
  -cookie value     web session cookie, required for TOP250 and actor pages
                    (default: $JAVDB_COOKIE)
  -token value      app JWT, returned by "javdbcli login"
                    (default: $JAVDB_TOKEN)
  -proxy value      http/socks5 proxy url
  -locale value     zh-TW (default), zh-CN or en
  -timeout duration per-request timeout (default 20s)
  -ttl duration     result cache TTL, 0 disables caching (default 10m)
  -rps float        requests per second per backend (default 2)
  -retries int      retries after a transient failure (default 2)
  -v                log failover and retry decisions

  -json             print the decoded structures
  -h                command help
`)
}

// commonFlags are shared by every sub-command.
type commonFlags struct {
	sites    stringList
	api      string
	cookie   string
	token    string
	proxy    string
	locale   string
	timeout  time.Duration
	ttl      time.Duration
	rps      float64
	retries  int
	verbose  bool
	asJSON   bool
	page     int
	limit    int
	category string
}

type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// values returns the trimmed, non-empty entries: -site "" is therefore a
// deliberate "no sites" rather than "use the defaults".
func (l stringList) values() []string {
	out := make([]string, 0, len(l))
	for _, item := range l {
		for _, part := range strings.Split(item, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.Var(&c.sites, "site", "web mirror, repeatable (empty -site disables the web backend)")
	fs.StringVar(&c.api, "api", javdb.DefaultAPIBase, "mobile JSON API root (\"\" disables it)")
	fs.StringVar(&c.cookie, "cookie", os.Getenv("JAVDB_COOKIE"), "web session cookie")
	fs.StringVar(&c.token, "token", os.Getenv("JAVDB_TOKEN"), "app JWT")
	fs.StringVar(&c.proxy, "proxy", os.Getenv("JAVDB_PROXY"), "http/socks5 proxy url")
	fs.StringVar(&c.locale, "locale", "zh-TW", "site locale: zh-TW, zh-CN, en")
	fs.DurationVar(&c.timeout, "timeout", 20*time.Second, "per-request timeout")
	fs.DurationVar(&c.ttl, "ttl", 10*time.Minute, "result cache TTL, 0 to disable")
	fs.Float64Var(&c.rps, "rps", 2, "requests per second per backend")
	fs.IntVar(&c.retries, "retries", 2, "retries after a transient failure")
	fs.BoolVar(&c.verbose, "v", false, "log failover and retry decisions")
	fs.BoolVar(&c.asJSON, "json", false, "print JSON instead of a table")
	fs.IntVar(&c.page, "page", 1, "page number")
	fs.IntVar(&c.limit, "limit", 0, "page size, 0 uses the backend default")
	fs.StringVar(&c.category, "category", "", "bucket: all, censored, uncensored, western, fc2, amateur, anime, chinese")
}

func (c *commonFlags) client() (*javdb.Client, error) {
	opts := []javdb.Option{
		javdb.WithAPIBase(c.api),
		javdb.WithCookie(c.cookie),
		javdb.WithAppToken(c.token),
		javdb.WithProxy(c.proxy),
		javdb.WithLocale(c.locale),
		javdb.WithTimeout(c.timeout),
		javdb.WithCache(c.ttl),
		javdb.WithRateLimit(c.rps),
		javdb.WithRetry(c.retries, 800*time.Millisecond),
	}
	if sites := c.sites.values(); len(sites) > 0 || len(c.sites) > 0 {
		opts = append(opts, javdb.WithSites(sites...))
	}
	if c.verbose {
		opts = append(opts, javdb.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	}
	client, err := javdb.New(opts...)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "backends: %s (site %s)\n", strings.Join(client.Backends(), " > "), client.Site())
	return client, nil
}

func (c *commonFlags) pageParams() javdb.Page {
	return javdb.Page{Page: c.page, Limit: c.limit}
}

func (c *commonFlags) cat() javdb.Category {
	return javdb.Category(c.category)
}

// dump prints v as JSON when -json was given; callers return true to skip
// their human-readable rendering.
func (c *commonFlags) dump(v any) bool {
	if !c.asJSON {
		return false
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "javdbcli: encode: %v\n", err)
	}
	return true
}

func newFlagSet(name string) (*flag.FlagSet, *commonFlags) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	c := &commonFlags{}
	c.register(fs)
	return fs, c
}

// boolValue mirrors the unexported interface the flag package uses to tell
// value-less flags (-v, -json) from flags that consume the next argument.
type boolValue interface {
	IsBoolFlag() bool
}

// parseArgs accepts flags anywhere in the command line: `search ssis -limit 5`
// works as well as the `search -limit 5 ssis` form Go's flag package insists on
// (it stops parsing at the first positional argument).
func parseArgs(fs *flag.FlagSet, args []string) error {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if len(arg) < 2 || arg[0] != '-' {
			rest = append(rest, arg)
			continue
		}
		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.ContainsAny(name, "=") {
			continue // -flag=value: nothing more to consume
		}
		f := fs.Lookup(name)
		if f == nil {
			continue // unknown flag: let flag.Parse report it in place
		}
		if bv, ok := f.Value.(boolValue); !ok || !bv.IsBoolFlag() {
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		}
	}
	return fs.Parse(append(flags, rest...))
}

func runSearch(args []string) error {
	fs, c := newFlagSet("search")
	var (
		sortBy = fs.String("sort", "", "relevance, newest, oldest, highest, lowest, most_magnets, most_played, most_watched")
		filter = fs.String("filter", "", "all, c (subtitled), p (playable), m (downloadable), s (single), nowatched")
		sub    = fs.Bool("sub", false, "only entries with Chinese-subtitled torrents")
		recent = fs.Bool("recent", false, "search the recent window instead of the archive")
		year   = fs.Int("year", 0, "restrict release year")
		scope  = fs.String("scope", "movie", "movie, actor or tag")
		tags   = fs.String("tag", "", "tag filter as category:id, repeatable with -tag 4:15 -tag 1:3")
	)
	_ = parseArgs(fs, args)
	if fs.NArg() == 0 {
		return errors.New("search needs a keyword (flags may appear anywhere)")
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	query := javdb.Query{
		Keyword:      strings.Join(fs.Args(), " "),
		Scope:        javdb.SearchScope(*scope),
		Category:     c.cat(),
		Sort:         javdb.SortBy(*sortBy),
		Filter:       javdb.FilterBy(*filter),
		WithSubtitle: *sub,
		FromRecent:   *recent,
		Year:         *year,
		TagIDs:       parseTags(*tags),
		Page:         c.pageParams(),
	}
	ctx := context.Background()
	result, err := client.Search(ctx, query)
	if err != nil {
		return err
	}
	if c.dump(result) {
		return nil
	}
	fmt.Printf("search %q via %s: page %s\n", query.Keyword, orDefault(result.Source, "?"), pageCount(result.Current, result.MaxPage))
	if len(result.Movies) > 0 {
		printMovies(result.Movies)
	}
	if len(result.Actors) > 0 {
		printActors(result.Actors)
	}
	if len(result.Tags) > 0 {
		for _, t := range result.Tags {
			fmt.Printf("  [%s] %s\n", t.ID, t.Name)
		}
	}
	return nil
}

func runRanking(args []string) error {
	name := "playback"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	fs, c := newFlagSet("ranking")
	period := fs.String("period", "daily", "daily, weekly, monthly, yearly, all")
	year := fs.Int("year", 0, "year for top250 -kind year slices")
	err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	ctx := context.Background()
	var ranking *javdb.Ranking
	switch javdb.RankingKind(name) {
	case javdb.RankingPlayback:
		ranking, err = client.Playback(ctx, javdb.Period(*period), c.pageParams())
	case javdb.RankingMovies:
		ranking, err = client.CategoryRanking(ctx, javdb.Period(*period), c.cat(), c.pageParams())
	case javdb.RankingTop250:
		slice := javdb.Top250All
		if *year > 0 {
			slice = javdb.Top250OfYear(*year)
		}
		ranking, err = client.Top250(ctx, slice, c.pageParams())
	case javdb.RankingActors:
		ranking, err = client.ActorRanking(ctx, c.cat(), c.pageParams())
	case javdb.RankingFanzaAward:
		ranking, err = client.Ranking(ctx, javdb.RankingQuery{Kind: javdb.RankingFanzaAward, Page: c.pageParams()})
	default:
		return fmt.Errorf("unknown ranking %q (playback, movies, top250, actors, fanza)", name)
	}
	if err != nil {
		return err
	}
	if c.dump(ranking) {
		return nil
	}
	fmt.Printf("%s ranking via %s (period %s, category %s)\n", ranking.Kind, orDefault(ranking.Source, "?"), orDefault(ranking.Period, "-"), orDefault(ranking.Category, "-"))
	if len(ranking.Movies) > 0 {
		printMovies(ranking.Movies)
	}
	if len(ranking.Actors) > 0 {
		printActors(ranking.Actors)
	}
	return nil
}

func runMovie(args []string) error {
	fs, c := newFlagSet("movie")
	wantMagnets := fs.Bool("magnets", false, "include the torrent table")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("movie needs an id or a video code")
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	ctx := context.Background()
	token := fs.Arg(0)
	detail, err := client.Movie(ctx, token)
	if err != nil {
		return err
	}
	if *wantMagnets && !detail.MagnetsFetched {
		if magnets, err := client.Magnets(ctx, detail.ID); err == nil {
			detail.Magnets = magnets
		} else {
			fmt.Fprintf(os.Stderr, "magnets: %v\n", explain(err))
		}
	}
	if c.dump(detail) {
		return nil
	}
	printDetail(detail, client.Site())
	return nil
}

func runMagnets(args []string) error {
	fs, c := newFlagSet("magnets")
	best := fs.Bool("best", false, "print only the recommended torrent")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("magnets needs an id or a video code")
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if *best {
		magnet, err := client.BestMagnet(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		if c.dump(magnet) {
			return nil
		}
		printMagnets([]javdb.Magnet{*magnet})
		fmt.Println(magnet.MagnetURL())
		return nil
	}
	magnets, err := client.Magnets(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if c.dump(magnets) {
		return nil
	}
	printMagnets(magnets)
	return nil
}

func runBrowse(args []string) error {
	fs, c := newFlagSet("browse")
	code := fs.String("code", "", "video code prefix -> /video_codes/<CODE>")
	maker := fs.String("maker", "", "maker id or name -> /makers/<id>")
	series := fs.String("series", "", "series id or name")
	publisher := fs.String("publisher", "", "publisher id or name")
	director := fs.String("director", "", "director id or name")
	actor := fs.String("actor", "", "actor id -> /actors/<id> filmography")
	tag := fs.String("tag", "", "tag filter as category:id, e.g. 4:15")
	download := fs.Bool("downloadable", false, "only entries with torrents")
	subtitle := fs.Bool("sub", false, "only entries with subtitled torrents")
	if err := parseArgs(fs, args); err != nil {
		return err
	}

	query := javdb.CategoryQuery{
		Category:         c.cat(),
		VideoCode:        *code,
		Maker:            *maker,
		Series:           *series,
		Publisher:        *publisher,
		Director:         *director,
		ActorID:          *actor,
		TagIDs:           parseTags(*tag),
		DownloadableOnly: *download,
		WithSubtitle:     *subtitle,
		Page:             c.pageParams(),
	}
	if query.Category == "" && query.VideoCode == "" && query.Maker == "" && query.Series == "" &&
		query.Publisher == "" && query.Director == "" && query.ActorID == "" && len(query.TagIDs) == 0 {
		return errors.New("browse needs one of -category, -code, -maker, -series, -publisher, -director, -actor or -tag")
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	result, err := client.CategoryMovies(context.Background(), query)
	if err != nil {
		return err
	}
	if c.dump(result) {
		return nil
	}
	fmt.Printf("listing via %s: page %s\n", orDefault(result.Source, "?"), pageCount(result.Current, result.MaxPage))
	printMovies(result.Movies)
	return nil
}

func runActor(args []string) error {
	fs, c := newFlagSet("actor")
	movies := fs.Bool("movies", false, "list the filmography instead of the profile")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("actor needs a stage name or an id")
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	ctx := context.Background()
	token := strings.Join(fs.Args(), " ")
	if *movies {
		result, err := client.ActorMovies(ctx, token, c.pageParams())
		if err != nil {
			return err
		}
		if c.dump(result) {
			return nil
		}
		printMovies(result.Movies)
		return nil
	}
	actor, err := client.Actor(ctx, token)
	if err != nil {
		return err
	}
	if c.dump(actor) {
		return nil
	}
	fmt.Printf("%s (%s) via %s\n", actor.Name, actor.ID, orDefault(actor.Source, "?"))
	if actor.AvatarURL != "" {
		fmt.Printf("  avatar    %s\n", actor.AvatarURL)
	}
	cup := ""
	if actor.Cup != "" {
		cup = " cup " + actor.Cup
	}
	fmt.Printf("  birthday  %s  height %dcm  measurements %d-%d-%d%s  from %s\n",
		orDefault(actor.Birthday, "-"), actor.Height, actor.Bust, actor.Waist, actor.Hips,
		cup, orDefault(actor.Birthplace, "-"))
	if actor.VideosCount > 0 {
		fmt.Printf("  videos    %d\n", actor.VideosCount)
	}
	if actor.NameTraditional != "" && actor.NameTraditional != actor.Name {
		fmt.Printf("  繁體      %s\n", actor.NameTraditional)
	}
	if actor.OtherName != "" {
		fmt.Printf("  aka       %s\n", actor.OtherName)
	}
	if actor.Twitter != "" {
		fmt.Printf("  twitter   %s\n", actor.Twitter)
	}
	return nil
}

func runTags(args []string) error {
	fs, c := newFlagSet("tags")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	groups, err := client.TagGroups(context.Background(), c.cat())
	if err != nil {
		return err
	}
	if c.dump(groups) {
		return nil
	}
	for _, g := range groups {
		fmt.Printf("%s (/tags?c%s=<id>)\n", g.Name, g.CategoryID)
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for _, o := range g.Options {
			fmt.Fprintf(w, "  %s\t%s\n", o.ID, o.Name)
		}
		w.Flush()
	}
	return nil
}

func runReviews(args []string) error {
	fs, c := newFlagSet("reviews")
	sortBy := fs.String("sort", "", "relevance (default) or latest")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("reviews needs an id or a video code")
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	page, err := client.Reviews(context.Background(), javdb.ReviewQuery{
		MovieID: fs.Arg(0),
		Sort:    javdb.SortBy(*sortBy),
		Page:    c.pageParams(),
	})
	if err != nil {
		return err
	}
	if c.dump(page) {
		return nil
	}
	fmt.Printf("comments via %s (%d total)\n", orDefault(page.Source, "?"), page.Total)
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for _, r := range page.Reviews {
		summary := strings.ReplaceAll(strings.TrimSpace(r.Content), "\n", " ")
		if len(summary) > 120 {
			summary = summary[:120] + "…"
		}
		fmt.Fprintf(w, "%s\t%.1f\t%s\t%s\n", orDefault(r.Author, "-"), r.Rating, orDefault(r.Date, "-"), summary)
	}
	return w.Flush()
}

// runLogin exchanges account credentials for the mobile-app JWT and prints it,
// so later runs can reuse the session through -token or $JAVDB_TOKEN.
//
// The HTML site has no usable password endpoint (the sign-in form sits behind
// Cloudflare and needs a verified account), so web scraping keeps requiring an
// imported -cookie; this command only unlocks the app side, i.e. TOP250.
func runLogin(args []string) error {
	fs, c := newFlagSet("login")
	user := fs.String("user", os.Getenv("JAVDB_USER"), "account email or username")
	pass := fs.String("pass", os.Getenv("JAVDB_PASSWORD"), "account password (avoids the shell history)")
	passFile := fs.String("pass-file", "", "read the password from a file, or - for stdin")
	verify := fs.Bool("verify", true, "read TOP250 to prove the session is accepted")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	if *passFile != "" {
		secret, err := readSecret(*passFile)
		if err != nil {
			return err
		}
		*pass = secret
	}
	if *user == "" || *pass == "" {
		return errors.New(`login needs -user and -pass: read -pass from $JAVDB_PASSWORD, or pipe it in with -pass-file - (printf '%s' "$pw" | javdbcli login -user a@b -pass-file -)`)
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err := client.Login(ctx, javdb.Credentials{Username: *user, Password: *pass}); err != nil {
		return err
	}
	_, token := client.Session()
	fmt.Printf("app JWT obtained\n\nexport JAVDB_TOKEN='%s'\n", token)
	if !*verify {
		return nil
	}
	r, err := client.Ranking(ctx, javdb.RankingQuery{Kind: javdb.RankingTop250, Page: javdb.Page{Page: 1, Limit: 5}})
	if err != nil {
		return fmt.Errorf("login worked but TOP250 still fails: %w", err)
	}
	fmt.Printf("\nTOP250 via %s:\n", r.Source)
	printMovies(r.Movies)
	return nil
}

// readSecret returns the trimmed contents of path ("-" reads stdin), so a
// password never has to appear in the process arguments.
func readSecret(path string) (string, error) {
	var src io.Reader = os.Stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		src = f
	}
	raw, err := io.ReadAll(io.LimitReader(src, 1<<13))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// ---------------------------------------------------------------------------
// rendering
// ---------------------------------------------------------------------------

func printMovies(movies []javdb.Movie) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "#\t番号\t標題\t日期\t評分\t磁鏈\t演員")
	for _, m := range movies {
		label := m.ID
		if m.Ranking > 0 {
			label = fmt.Sprintf("%d", m.Ranking)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			label,
			orDefault(m.Code, "-"),
			truncate(firstLine(m.Title), 48),
			orDefault(m.ReleaseDate, "-"),
			rateCell(m),
			magnetsCell(m),
			strings.Join(m.Actors, "/"),
		)
	}
	w.Flush()
}

func rateCell(m javdb.Movie) string {
	if m.Score == 0 {
		return "-"
	}
	if m.Ratings > 0 {
		return fmt.Sprintf("%.2f (%d)", m.Score, m.Ratings)
	}
	return fmt.Sprintf("%.2f", m.Score)
}

func magnetsCell(m javdb.Movie) string {
	cells := "-"
	if m.MagnetsCount > 0 {
		cells = fmt.Sprintf("%d", m.MagnetsCount)
	}
	switch {
	case m.HasCNSub:
		cells += " 中字"
	case m.CanPlay:
		cells += " 可播"
	}
	return cells
}

func printActors(actors []javdb.Actor) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "#\t演員\tid\t作品數")
	for _, a := range actors {
		label := a.ID
		if a.Ranking > 0 {
			label = fmt.Sprintf("%d", a.Ranking)
		}
		// Ranking rows carry no filmography count, so "-" beats a misleading 0.
		count := "-"
		if a.VideosCount > 0 {
			count = fmt.Sprintf("%d", a.VideosCount)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", label, truncate(a.Name, 30), a.ID, count)
	}
	w.Flush()
}

func printDetail(d *javdb.Detail, site string) {
	fmt.Printf("%s  %s\n", orDefault(d.Code, d.ID), d.Title)
	if d.OriginTitle != "" && d.OriginTitle != d.Title {
		fmt.Printf("  原名    %s\n", d.OriginTitle)
	}
	fmt.Printf("  url     %s\n", d.URL(site))
	line := func(label, value string) {
		if value != "" {
			fmt.Printf("  %-7s  %s\n", label, value)
		}
	}
	line("發行", orDefault(linkName(d.Maker), "-")+" / "+orDefault(linkName(d.Publisher), "-"))
	line("系列", linkName(d.Series))
	line("导演", linksToString(d.Directors))
	line("日期", d.ReleaseDate)
	line("片長", fmt.Sprintf("%d 分鐘", d.Duration))
	line("評分", rateCell(d.Movie))
	line("想看", fmt.Sprintf("%d 人想看 / %d 人看過", d.WantCount, d.WatchedCount))
	line("評論", fmt.Sprintf("%d 則", d.ReviewsCount))
	line("演員", actorsToString(d.ActorCredits))
	line("類別", linksToString(d.Genres))
	if d.Summary != "" {
		line("簡介", firstLine(d.Summary))
	}
	line("預覽", d.PreviewVideo)
	if len(d.Magnets) > 0 {
		fmt.Println("  磁鏈:")
		printMagnets(d.Magnets)
	}
}

func printMagnets(magnets []javdb.Magnet) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "名稱\t大小\t檔案\t日期\t標籤")
	for _, m := range magnets {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
			truncate(m.Name, 60),
			orDefault(m.SizeText, "-"),
			m.Files,
			orDefault(m.CreatedAt, "-"),
			strings.Join(append([]string{m.Category()}, m.Tags...), " "))
	}
	w.Flush()
}

func linkName(l *javdb.Link) string {
	if l == nil {
		return ""
	}
	return l.Name
}

func linksToString(links []javdb.Link) string {
	names := make([]string, 0, len(links))
	for _, l := range links {
		names = append(names, l.Name)
	}
	return strings.Join(names, ", ")
}

func actorsToString(actors []javdb.Actor) string {
	names := make([]string, 0, len(actors))
	for _, a := range actors {
		names = append(names, a.Name)
	}
	return strings.Join(names, ", ")
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// parseTags turns "4:15,1:3" or repeated -tag values into a tag filter map.
func parseTags(raw string) map[string]string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	out := map[string]string{}
	for _, pair := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			fmt.Fprintf(os.Stderr, "javdbcli: ignoring malformed -tag %q (want category:id)\n", pair)
			continue
		}
		out[strings.TrimPrefix(parts[0], "c")] = parts[1]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// explain turns sentinel errors into actionable advice.
func explain(err error) error {
	switch {
	case errors.Is(err, javdb.ErrAuthRequired):
		return fmt.Errorf("%w\n  → pass -cookie (web session) or -token (app JWT, see \"javdbcli login\")", err)
	case errors.Is(err, javdb.ErrRateLimited):
		return fmt.Errorf("%w\n  → lower -rps or retry later, JavDB throttles aggressive clients", err)
	case errors.Is(err, javdb.ErrChallenge):
		return fmt.Errorf("%w\n  → Cloudflare interstitial: try another -site mirror or a -proxy", err)
	case errors.Is(err, javdb.ErrUnsupported):
		return fmt.Errorf("%w\n  → the capability exists on the other backend; check -api/-site", err)
	}
	return err
}

func exitCode(err error) int {
	switch {
	case errors.Is(err, javdb.ErrNotFound), errors.Is(err, javdb.ErrEmptyResult):
		return 3
	case errors.Is(err, javdb.ErrAuthRequired):
		return 4
	default:
		return 1
	}
}

// pageCount renders "1/20", or "1/-" when the backend does not report how many
// pages exist - the app search endpoint answers only current_page.
func pageCount(current, max int) string {
	if max <= 0 {
		return fmt.Sprintf("%d/-", current)
	}
	return fmt.Sprintf("%d/%d", current, max)
}

// orDefault renders an optional string-ish field (Category, Period, a date ...)
// with a placeholder, so the tables never show empty columns.
func orDefault[T ~string](s T, def string) string {
	if string(s) == "" {
		return def
	}
	return string(s)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i]) + "…"
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
