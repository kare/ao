/*
ao is an acme program for querying the go oracle tool. When run, ao will start
a new window with a menu of queries to be run on the invoking window. A query
can be executed by using the middle button. ao will then use the selection
as an argument to the oracle tool.

If ao is run without arguments, the file of the invoking window will be used
as scope. If an ao instance is already running, it will be switched to the
new invoking window. An ao instance will exit once its window has been closed.
*/
package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"code.google.com/p/goplan9/plan9/acme"

	"code.google.com/p/go.tools/go/loader"
	"code.google.com/p/go.tools/oracle"
)

var modes = `Current file is: %s
callees
callers 
callgraph 
callstack 
peers
pointsto

definition
describe 
freevars 

implements 
referrers
`

var ld = loader.Config{SourceImports: true}

func fatalln(x ...interface{}) {
	fmt.Fprintln(os.Stderr, x...)
	os.Exit(1)
}

func main() {
	flag.Parse()
	// initial window
	winid := os.Getenv("winid")
	if winid == "" {
		fatalln("ao run outside acme window")
	}

	c, err := net.Dial("unix", "/tmp/ao")
	if err == nil {
		// ao already running. Invocation is changewindows
		sendChangeMessage(c, winid)
		return
	}
	scope := getScope(flag.Args(), winid)
	_, err = ld.FromArgs(scope, false)
	if err != nil {
		fatalln(err)
	}
	prog, err := ld.Load()
	if err != nil {
		fatalln(err)
	}
	oracl, err := oracle.New(prog, nil, false)
	if err != nil {
		fatalln("Cannot create oracle: ", err)
	}
	win, err := acme.New()
	if err != nil {
		fatalln("Cannot create acme window: ", err)
	}
	win.Name("/ao/%s", scope[0])
	writeModes(win, winid)
	win.Ctl("clean")
	dr := dataReader{win}
	evch := win.EventChan()

	os.Remove("/tmp/ao")
	l, err := net.Listen("unix", "/tmp/ao")
	if err != nil {
		fatalln("Cannot listen for clients: ", err)
	}
	lch := make(chan net.Conn)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				fatalln("Cannot listen for clients: ", err)
			}
			lch <- c
		}
	}()

	for {
		var mode string
		select {
		case e, ok := <-evch:
			if !ok {
				os.Exit(0)
			}
			if e.C1 == 'M' && e.C2 == 'X' {
				// middle click on one of the modes, query the oracle
				mode = string(e.Text)
				win.Addr(",")
				win.Write("data", []byte("querying oracle\n"))
				fname, b0, b1 := getPositionInfo(winid)
				posStr := fmt.Sprintf("%s:#%d,#%d", fname, b0, b1)
				qp, err := oracle.ParseQueryPos(prog, posStr, false)
				if err != nil {
					fatalln("Cannot get position: ", err)
				}

				res, err := oracl.Query(mode, qp)
				if err != nil {
					writeModes(win, winid)
					fmt.Fprintln(dr, "Cannot query oracle: ", err)
					win.Ctl("clean")
					continue
				}
				writeModes(win, winid)
				res.WriteTo(dr)
				win.Ctl("clean")
			} else if e.Flag&1 != 0 {
				win.WriteEvent(e)
			}
		case c := <-lch:
			b := bufio.NewReader(c)
			str := changeWindow(b)
			if str == "" {
				panic("bad connection")
			}
			winid = str
			c.Close()
			writeModes(win, winid)
			win.Ctl("clean")
		}
	}
}

func changeWindow(b *bufio.Reader) (winid string) {
	idstr, err := b.ReadString('\n')
	if err != nil {
		panic(err)
	}
	winid = idstr[:len(idstr)-1]
	return winid

}

func sendChangeMessage(c net.Conn, winid string) {
	// message format is as follows
	// The window number to switch to in ASCII, followed by a newline
	_, err := fmt.Fprintln(c, winid)
	if err != nil {
		fatalln("cannot change window: ", err)
	}
	c.Close()
}
func getScope(arg []string, winid string) []string {
	if len(arg) == 0 {
		arg = []string{"."}
	}
	scope := make([]string, len(arg))
	for i, s := range arg {
		var scp string
		scp = s
		if s == "." {
			fname, _, _ := getPositionInfo(winid)
			scp = fname
		}
		scope[i] = scp
	}
	return scope
}

func getPositionInfo(idstr string) (name string, b0 int, b1 int) {
	id, err := strconv.ParseInt(idstr, 10, 0)
	if err != nil {
		panic("non-numerical winid" + err.Error() + idstr)
	}
	win, err := acme.Open(int(id), nil)
	if err != nil {
		fatalln("Cannot open acme window: ", err)
	}
	defer win.CloseFiles()
	name = getFilename(win)
	if isDirty(win) {
		fatalln("window must be non-dirty to query. Save the file and try again")
	}

	// acme will initialize addr on first open, if you do addr=dot before opening the addr file
	// you'll get zeroes back. Do a dummy read to get around this.
	_, _, _ = win.ReadAddr()

	err = win.Ctl("addr=dot")
	if err != nil {
		fatalln("Cannot read acme address: ", err)
	}

	// find rune offset
	q0, q1, err := win.ReadAddr()
	if err != nil {
		fatalln("Cannot read acme address: ", err)
	}
	b0, b1 = runeToByte(win, q0, q1)
	return name, b0, b1
}

func runeToByte(win *acme.Win, q0, q1 int) (b0, b1 int) {
	// convert rune offsets to byte offsets
	err := win.Addr("0")
	if err != nil {
		fatalln("Cannot decode unicode: ", err)
	}
	dr := dataReader{win}
	br := bufio.NewReader(dr)
	b0 = 0
	for i := 0; i < q0; i++ {
		_, sz, err := br.ReadRune()
		if err != nil {
			panic("whut")
		}
		b0 += sz
	}
	b1 = b0
	for i := q0; i < q1; i++ {
		_, sz, err := br.ReadRune()
		if err != nil {
			panic("whut")
		}
		b1 += sz
	}
	return
}

func isDirty(win *acme.Win) bool {
	ctl, err := win.ReadAll("ctl")
	if err != nil {
		panic("can't read info")
	}
	f := strings.Fields(string(ctl))
	if len(f) == 0 {
		panic("can't read info")
	}
	return f[4] != "0"
}

func getFilename(win *acme.Win) string {
	// use current file scope
	s, err := win.ReadAll("tag")
	if err != nil {
		fatalln("Cannot get current file name: ", err)
	}
	f := strings.Fields(string(s))
	return f[0]
}

func writeModes(win *acme.Win, idstr string) {
	fname, _, _ := getPositionInfo(idstr)
	win.Addr(",")
	s := fmt.Sprintf(modes, fname)
	win.Write("data", []byte(s))
}

type dataReader struct {
	*acme.Win
}

func (d dataReader) Read(b []byte) (int, error) {
	return d.Win.Read("data", b)
}

func (d dataReader) Write(b []byte) (int, error) {
	return d.Win.Write("data", b)
}
