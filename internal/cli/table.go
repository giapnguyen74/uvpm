package cli

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/giapnguyen74/uvpm/internal/model"
)

func formatDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func printTable(w io.Writer, apps []model.AppInfo) {
	if len(apps) == 0 {
		fmt.Fprintln(w, "no apps")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tMODE\tPID\tSTATUS\tRESTARTS\tUPTIME")
	for _, a := range apps {
		pid, up := "-", "-"
		if a.Pid > 0 {
			pid = fmt.Sprint(a.Pid)
		}
		if a.Status == model.StatusOnline {
			up = formatDuration(a.UptimeMs)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%d\t%s\n", a.ID, a.Name, a.Mode, pid, a.Status, a.Restarts, up)
	}
	tw.Flush()
}

func printDescribe(w io.Writer, a model.AppInfo) {
	row := func(k string, v any) { fmt.Fprintf(w, "%-12s %v\n", k, v) }
	row("id", a.ID)
	row("name", a.Name)
	row("mode", a.Mode)
	row("status", a.Status)
	row("desired", a.Desired)
	row("pid", a.Pid)
	row("restarts", a.Restarts)
	if a.Status == model.StatusOnline {
		row("uptime", formatDuration(a.UptimeMs))
	}
	if a.LastExit != "" {
		row("last exit", a.LastExit)
	}
	if a.LastError != "" {
		row("last error", a.LastError)
	}
	row("cwd", a.Cwd)
	row("command", fmt.Sprint(a.Command))
	row("out log", a.OutLog)
	row("err log", a.ErrLog)
	row("autorestart", a.Spec.Autorestart)
	row("max restarts", a.Spec.MaxRestarts)
	for k, v := range a.Spec.Env {
		row("env", k+"="+v)
	}
}
