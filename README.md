# Sliplane CLI

## License

Copyright 2026 Sliplane GmbH. Licensed under the [Apache License, Version 2.0](LICENSE).

## PostgreSQL

```sh
sliplane postgres connect main
sliplane postgres query main 'SELECT now();'
sliplane postgres query main --file script.sql --timeout 30s
cat script.sql | sliplane postgres query main --timeout 1m
```

Both commands include [usql](https://github.com/xo/usql); neither `psql` nor `usql` needs to be installed. Connections use verified TLS and credentials from the Sliplane API.

The interactive shell supports multiline SQL, completion, session history, and usql commands such as `\dt`, `\d table`, `\x`, `\timing`, `\?`, and `\q`. It follows usql behavior rather than full psql compatibility (notably, `\copy` has different semantics). History stays in memory; startup files are not loaded. Query errors return a nonzero exit status. Both commands require `--output text` and use usql's table formatting.

For `query`, supply a quoted SQL argument, `--file` (`-f`), or pipe SQL to stdin when the argument is omitted. `--file -` explicitly selects stdin. A SQL argument and `--file` cannot be combined; empty input is rejected. Scripts stop on the first SQL error. `--timeout` accepts durations such as `500ms`, `30s`, or `2m` and limits the entire command, including input reading and connection setup. It defaults to `0` (no overall deadline); a timeout exits with an error.

Before connecting, the CLI checks your public IP against the database allowlist. If blocked, a terminal session asks before adding a persistent rule for that single IP, preserving existing entries. Redirected commands never prompt; add your IP in the dashboard first.

PostgreSQL integration tests can run against a disposable local server with `SLIPLANE_TEST_POSTGRES_BIN=/path/to/postgres/bin go test ./internal/cli -run TestPostgres`.
