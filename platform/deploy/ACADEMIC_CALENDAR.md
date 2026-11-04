# 校历与教学日提醒

`academic_sync.py` uses Python's standard library to read the school's official
XLSX, student holiday notices and the State Council policy search API. Each owner
has an independent `home/assistant/academic-calendar-seed.json`, compiled
`academic-calendar.json`, source cache and sync status. Run the sync as that
instance's Unix user; atomic files are mode 0600. No website code, model or
message sender is invoked by the updater.

Teaching precedence: term bounds → explicit school date adjustment → national
holiday/makeup dates → ordinary Monday–Friday. An explicit school closure wins
against a national working day. Makeup courses use the original teaching week
and weekday; personal course exceptions remain effective. Term teaching days
include exam/study weekdays even if the personal timetable has no class.

Residence: consecutive teaching days stay at school; leave at 19:00 before a
break, return at 17:00 on the day before teaching resumes. A single rest day
between teaching days stays at school. Explicit temporary location plans win.
These are estimates, not live location. Makeup days follow the source class
weekday's routine; without a school mapping they follow Monday's routine. Date
routine overrides remain the final gate for all automated messages.

The `schoolday_reminder` native module is disabled by default. This owner's
`schoolday_uu_pc` trigger enables it at 10:55 Asia/Shanghai daily, without a
weekday filter, with a 120-second execution window. The module itself checks the
calendar both when preparing and admitting delivery. It sends a fixed message,
uses the existing owner delivery target, and never invokes UU or a model. The
existing heartbeat occurrence ledger prevents duplicate delivery after restart.

A daily 04:20 systemd timer runs the updater. Unreachable or unparsable sources
retain cached valid documents. A partial annual notice cannot mark that year
verified. The imported 2026–2027 calendar covers 2026-09-14–2027-01-22 and
2027-03-01–2027-07-09. The 2027 national schedule is explicitly provisional until
a complete official notice is fetched. Check the course page for current status,
future dates, source links and update failures.

Example manual refresh (replace INSTANCE_HOME and run as its owner):

    /usr/bin/python3 /opt/asrobot-agent/academic_sync.py --home INSTANCE_HOME

Server release records and binary/config backups are stored in
`/opt/asrobot/releases/academic-calendar-20261104` and
`/var/backups/asrobot-academic-*`. Do not restore an old state.sqlite3 over a
running instance; rollback binaries and the versioned calendar files first.
