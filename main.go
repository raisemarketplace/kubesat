package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	termbox "github.com/nsf/termbox-go"

	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/raisemarketplace/kubesat/db"
	"github.com/raisemarketplace/kubesat/logger"
	"github.com/raisemarketplace/kubesat/proc"
	"github.com/raisemarketplace/kubesat/termbox/kit"
)

// const Banana rune = '⦅' // unicode left white paren
const Banana rune = '❪' // unicode MEDIUM FLATTENED LEFT PARENTHESIS ORNAMENT

// BananaColors for termbox.Output256
// https://en.wikipedia.org/wiki/ANSI_escape_code#8-bit
var BananaColors = [6]termbox.Attribute{
	0x2f, // very green
	0x9b, // yellowish-green
	0xbf, // not quite ripe
	0xb9, // ripe
	0x89, // brownish-yellow
	0x5f, // banana bread
}

// BananaAges where anything less than the given age corresponds to
// the same index in BananaColors, with everything else falling to the
// last color.
var BananaAges = [5]time.Duration{
	10 * time.Minute,
	30 * time.Minute,
	4 * time.Hour,
	12 * time.Hour,
	5 * 24 * time.Hour,
}

// AgeColor returns a color for termbox.Output256 for the given age on
// an arbitrary "banana" scale.
func AgeColor(age time.Duration) termbox.Attribute {
	for i, cutoff := range BananaAges {
		if age < cutoff {
			return BananaColors[i]
		}
	}
	return BananaColors[len(BananaColors)-1]
}

type State struct {
	Grid     *kit.Grid
	Snapshot db.Snapshot
	Selected string
	Logger   *logger.Logger
	Procs    []proc.Proc

	Clientset *kubernetes.Clientset
	EC2       *ec2.EC2
}

func (s *State) SelectMove(inc int) {
	index := 0

	if _, i, found := s.Snapshot.NodeTable.ByAwsID(s.Selected); found {
		index = i + inc
	}
	if index >= 0 && index < len(s.Snapshot.NodeTable.Rows) {
		s.Selected = s.Snapshot.NodeTable.Rows[index].AwsID
	}
}

func (s *State) SelectDown() {
	s.SelectMove(1)
}

func (s *State) SelectUp() {
	s.SelectMove(-1)
}

func Optional(b bool, ifTrue string) string {
	if b {
		return ifTrue
	}
	return ""
}

func Update(state *State, buf kit.BufferSlice) {
	grid := state.Grid
	grid.Clear()

	topline := make(kit.Line, 0, 0)
	for _, color := range BananaColors {
		topline = append(topline, kit.Cell{Banana, color, 0})
	}
	topline = append(topline, kit.String(fmt.Sprintf("%s-%s", Program, Version)))
	topline = append(topline, kit.String(" | press 'q' to quit"))
	grid.Items["topline"] = topline

	{
		rows := make([]kit.TableRow, 0, len(state.Procs))
		for _, proc := range state.Procs {
			msg, isRunning := proc.Status()
			icon := func() string {
				if isRunning {
					return "◷"
				} else {
					return "✓"
				}
			}()
			s := kit.String(fmt.Sprintf("%s %s: %s", icon, proc.Name(), msg))
			rows = append(rows, kit.Row(s))
		}
		grid.Items["procs"] = &kit.Table{Rows: rows}
	}

	cses := make([]string, len(state.Snapshot.ComponentStatuses))
	for i, cs := range state.Snapshot.ComponentStatuses {
		cses[i] = fmt.Sprintf("%s:%s", cs.Name, cs.Status)
	}
	grid.Items["component"] = kit.String(strings.Join(cses, "  "))

	padding := kit.Rune(' ')

	header := kit.Row(
		kit.Rune(' '), // master node or not
		kit.Rune(' '), // controller-manager leader
		kit.Rune(' '), // scheduler leader
		padding,
		kit.Rune(' '), // cordoned
		padding,
		kit.String("name"),
		padding,
		kit.Rune('◷'),
		padding,
		kit.String("Pend/Run/Err"), // pending, running, running-with-error
		padding,
		kit.String("version"),
		padding,
		kit.String("aws-id"),
		padding,
		kit.String("image-id"),
		padding,
		kit.String("image-version"),
		padding,
		kit.String("aws-state"))
	header.Bg = termbox.ColorBlack | termbox.AttrBold

	table := kit.Table{Rows: []kit.TableRow{header}}

	counts := state.Snapshot.ClusterPodCounts
	grid.Items["podcounts"] = kit.Line{
		// FIXME: pods count
		kit.AttrString{fmt.Sprintf("pods:%d  ", counts.Total), termbox.ColorBlue, 0},
		kit.AttrString{fmt.Sprintf("pending:%d  ", counts.Pending), termbox.ColorYellow, 0},
		kit.AttrString{fmt.Sprintf("running:%d  ", counts.Running), termbox.ColorBlue, 0},
		kit.AttrString{fmt.Sprintf("succeeded:%d  ", counts.Succeeded), termbox.ColorBlue, 0},
		kit.AttrString{fmt.Sprintf("failed:%d  ", counts.Failed), termbox.ColorRed, 0},
	}

	if state.Snapshot.NodeTable != nil {
		if len(state.Snapshot.NodeTable.Rows) > 0 {
			grid.Items["nodecount"] = kit.Line{
				kit.AttrString{fmt.Sprintf("nodes:%d", state.Snapshot.NodeCount),
					termbox.ColorCyan, 0},
				kit.String(fmt.Sprintf("  cluster:%s", state.Snapshot.ClusterName)),
			}
		}

		for _, data := range state.Snapshot.NodeTable.Rows {
			row := kit.Row(
				kit.String(Optional(data.IsMaster, "m")),
				kit.String(Optional(data.IsControllerManagerLeader, "c")),
				kit.String(Optional(data.IsSchedulerLeader, "s")),
				padding,
				kit.String(Optional(data.IsCordoned, "C")),
				padding,
				kit.String(data.Name),
				padding,
				kit.Cell(termbox.Cell{Banana, AgeColor(time.Since(data.CreatedAt)), 0}),
				padding,
				kit.String(fmt.Sprintf("%d/%d/%d", data.PodCounts.Pending, data.PodCounts.Running, data.PodCounts.WithError)),
				padding,
				kit.String(data.KubeletVersion),
				padding,
				kit.String(data.AwsID),
				padding,
				kit.String(data.ImageID),
				padding,
				kit.String(data.ImageVersion),
				padding,
				kit.String(data.AwsState))

			if data.AwsID == state.Selected {
				row.Fg = termbox.ColorBlack
				row.Bg = termbox.ColorYellow
			}

			table.Rows = append(table.Rows, row)
		}
	}

	grid.Items["main"] = &table

	{
		len := state.Logger.Len()
		rows := make([]kit.TableRow, 0, len)
		for i := 0; i < len; i++ {
			s := kit.AttrString{fmt.Sprintf("%s", state.Logger.At(i).Message), termbox.ColorRed, 0}
			rows = append(rows, kit.Row(s))
		}
		grid.Items["log"] = &kit.Table{Rows: rows}
	}

	grid.Draw(buf)
}

func runDeleteNode(state *State) {
	if state.Selected == "" {
		return
	}

	for _, row := range state.Snapshot.NodeTable.Rows {
		if row.AwsID == state.Selected {
			name := row.Name
			if name == "" {
				return
			}

			ctx := context.Background()
			state.Procs = append(state.Procs, proc.RunDeleteNode(ctx, state.Clientset, state.EC2, name))
			return
		}
	}
}

func main() {
	defaultKubeconfig := func() string {
		if user, err := user.Current(); err == nil && user.HomeDir != "" {
			return filepath.Join(user.HomeDir, ".kube", "config")
		}
		return ""
	}()
	showVersion := flag.Bool("version", false, "display version information")
	kubeconfig := flag.String("kubeconfig", defaultKubeconfig, "path to the kube config file")
	kubecontext := flag.String("context", "", "context within the kubeconfig to use")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s-%s\n", Program, Version)
		return
	}

	// kubernetes client config
	config, err := clientcmd.NewInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: *kubeconfig},
		&clientcmd.ConfigOverrides{CurrentContext: *kubecontext},
		os.Stdin).ClientConfig()
	if err != nil {
		log.Fatalf("error building kubeconfig: %s: %v", *kubeconfig, err)
	}

	// kubernetes clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("error creating kubernetes clientset: %v", err)
	}

	// aws client
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))
	ec2client := ec2.New(sess)

	loggerCapacity := 5
	logger := logger.New(context.TODO(), loggerCapacity)
	loggerUpdated := make(chan bool)
	logger.Updated.Subscribe(loggerUpdated)

	// gradually clear log buffer over time
	go func() {
		for {
			time.Sleep(30 * time.Second)
			logger.Infof("")
		}
	}()

	db := db.NewDB(logger, clientset, ec2client)

	areas := make(map[string]kit.Area)
	areas["topline"] = kit.AreaAt(0, 0).Span(1, 1).WidthFr(1).HeightCh(1)
	areas["podcounts"] = kit.AreaAt(0, 1).Span(1, 1).WidthFr(1).HeightCh(1)
	areas["nodecount"] = kit.AreaAt(0, 2).Span(1, 1).WidthFr(1).HeightCh(1)
	areas["component"] = kit.AreaAt(0, 3).Span(1, 1).WidthFr(1).HeightCh(1)
	areas["main"] = kit.AreaAt(0, 4).Span(1, 1).WidthFr(1).HeightFr(1)
	areas["procs"] = kit.AreaAt(0, 5).Span(1, 1).WidthFr(1).HeightFr(1)
	areas["log"] = kit.AreaAt(0, 6).Span(1, 1).WidthFr(1).HeightCh(loggerCapacity)
	grid := kit.NewGrid(areas)

	state := &State{
		Grid:      grid,
		Logger:    logger,
		Procs:     make([]proc.Proc, 0),
		Clientset: clientset,
		EC2:       ec2client,
	}

	if err := func() error {
		err := termbox.Init()
		if err != nil {
			return err
		}
		defer termbox.Close()

		termbox.SetOutputMode(termbox.Output256)
		termboxEvents := kit.StartPollEvents(context.TODO())

		exitSignal := make(chan os.Signal)
		signal.Notify(exitSignal, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)

		for {
			kit.Update(termbox.ColorWhite, termbox.ColorDefault, func(buf kit.BufferSlice) {
				Update(state, buf)
			})

			select {
			case <-exitSignal:
				return nil
			case ch := <-termboxEvents.Chars:
				switch ch {
				case 'q':
					return nil
				case 'D':
					runDeleteNode(state)
				case 'L':
					logger.Warnf("termbox: you pressed L at %s!", time.Now())
				}
			case key := <-termboxEvents.Keys:
				switch key {
				case termbox.KeyArrowDown:
					state.SelectDown()
				case termbox.KeyArrowUp:
					state.SelectUp()
				}
			case <-termboxEvents.Resizes:
				// update ui
			case <-termboxEvents.MouseCoords:
				// ignore
			case err := <-termboxEvents.Errors:
				logger.Warnf("termbox: %v", err)
			case <-termboxEvents.Interrupts:
				return nil
			case snapshot := <-db.Snapshots:
				state.Snapshot = snapshot
				if state.Selected == "" && len(snapshot.NodeTable.Rows) > 0 {
					state.Selected = snapshot.NodeTable.Rows[0].AwsID
				}
			case <-loggerUpdated:
				// update ui
			}
		}
	}(); err != nil {
		log.Fatalf("error initializing termbox: %v", err)
	}

	log.Printf("goodbye")
}
