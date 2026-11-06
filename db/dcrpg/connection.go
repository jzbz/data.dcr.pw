// Copyright (c) 2021, The Decred developers
// Copyright (c) 2017, The dcrdata developers
// See LICENSE for details.

package dcrpg

import (
	"database/sql"
	"os"
	"strings"

	_ "github.com/lib/pq" // Start the PostgreSQL driver
)

// quoteConnValue quotes a value for use in a key=value connection string,
// escaping backslashes and single quotes so that values containing spaces or
// quotes (e.g. passwords) cannot terminate the value early.
func quoteConnValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `'`, `\'`)
	return "'" + v + "'"
}

// connString builds a key=value connection string for lib/pq. When sslFromEnv
// is false, sslmode=disable is set explicitly. When it is true, sslmode is
// omitted so that lib/pq takes it, along with the related settings such as
// PGSSLROOTCERT, from the standard libpq environment variables.
func connString(host, port, user, pass, dbname string, sslFromEnv bool) string {
	opts := []string{
		"host=" + quoteConnValue(host),
		"user=" + quoteConnValue(user),
	}
	if pass != "" {
		opts = append(opts, "password="+quoteConnValue(pass))
	}
	opts = append(opts, "dbname="+quoteConnValue(dbname))
	if !sslFromEnv {
		opts = append(opts, "sslmode=disable")
	}

	// Only add port arg for TCP connections since UNIX domain sockets
	// (specified by a "/" prefix) do not have a port.
	if !strings.HasPrefix(host, "/") {
		opts = append(opts, "port="+quoteConnValue(port))
	}

	return strings.Join(opts, " ")
}

// Connect opens a connection to a PostgreSQL database. The caller is
// responsible for calling Close() on the returned db when finished using it.
// The input host may be an IP address for TCP connection, or an absolute path
// to a UNIX domain socket. An empty string should be provided for UNIX sockets.
//
// TLS is disabled unless the PGSSLMODE environment variable is set, in which
// case the connection uses that mode (e.g. require, verify-full) and the other
// standard libpq TLS environment variables (PGSSLROOTCERT, PGSSLCERT,
// PGSSLKEY). Set PGSSLMODE=verify-full when connecting to a PostgreSQL server
// on another host over an untrusted network.
func Connect(host, port, user, pass, dbname string) (*sql.DB, error) {
	sslFromEnv := os.Getenv("PGSSLMODE") != ""
	psqlInfo := connString(host, port, user, pass, dbname, sslFromEnv)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		return nil, err
	}

	return db, db.Ping()
}
