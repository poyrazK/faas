// Package functionsql provides a disposable, restricted TLS/SCRAM SQL witness
// for portable and native function acceptance. It is never a production driver.
package functionsql

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type Fixture struct {
	Admin    *pgxpool.Pool
	Role     string
	password string
}

func New(t *testing.T) *Fixture {
	t.Helper()
	f := &Fixture{Admin: pgtest.OpenDatabase(t), Role: "fn_" + strings.ReplaceAll(uuid.NewString(), "-", ""), password: strings.ReplaceAll(uuid.NewString(), "-", "")}
	if f.Admin == nil {
		t.Fatal("function SQL fixture requires an enabled disposable database")
	}
	var tls bool
	if err := f.Admin.QueryRow(t.Context(), "SELECT current_setting('ssl')='on'").Scan(&tls); err != nil || !tls {
		t.Fatal("function SQL fixture requires TLS")
	}
	role := pgx.Identifier{f.Role}.Sanitize()
	if _, err := f.Admin.Exec(t.Context(), "CREATE TABLE notes(id text PRIMARY KEY,pid integer NOT NULL)"); err != nil {
		t.Fatal("prepare function SQL witness")
	}
	if _, err := f.Admin.Exec(t.Context(), "CREATE ROLE "+role+" LOGIN NOINHERIT PASSWORD '"+f.password+"'"); err != nil {
		t.Fatal("create restricted function SQL role")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = f.Admin.Exec(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename=$1", f.Role)
		for _, sql := range []string{"DROP OWNED BY " + role, "DROP ROLE " + role} {
			if _, err := f.Admin.Exec(ctx, sql); err != nil {
				t.Error("clean restricted function SQL role")
			}
		}
	})
	for _, sql := range []string{"GRANT CONNECT ON DATABASE " + pgx.Identifier{f.Admin.Config().ConnConfig.Database}.Sanitize() + " TO " + role, "GRANT USAGE ON SCHEMA public TO " + role, "GRANT SELECT,INSERT ON notes TO " + role} {
		if _, err := f.Admin.Exec(t.Context(), sql); err != nil {
			t.Fatal("grant restricted function SQL access")
		}
	}
	var scram bool
	if err := f.Admin.QueryRow(t.Context(), "SELECT rolpassword LIKE 'SCRAM-SHA-256$%' FROM pg_authid WHERE rolname=$1", f.Role).Scan(&scram); err != nil || !scram {
		t.Fatal("function SQL fixture requires SCRAM")
	}
	return f
}

// ConnectionString is private test material: callers must never log it.
func (f *Fixture) ConnectionString(host string, port uint16) string {
	c := f.Admin.Config().ConnConfig
	if host == "" {
		host = c.Host
	}
	if port == 0 {
		port = c.Port
	}
	return (&url.URL{Scheme: "postgres", Host: net.JoinHostPort(host, strconv.Itoa(int(port))), Path: "/" + c.Database, User: url.UserPassword(f.Role, f.password), RawQuery: "sslmode=verify-full"}).String()
}

func (f *Fixture) Lock(ctx context.Context, t *testing.T) pgx.Tx {
	t.Helper()
	tx, err := f.Admin.Begin(ctx)
	if err != nil {
		t.Fatal("start independent SQL lock")
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	})
	if _, err := tx.Exec(ctx, "LOCK TABLE notes IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal("hold independent SQL lock")
	}
	return tx
}

func (f *Fixture) Connections(ctx context.Context, t *testing.T) (all, blocked int) {
	t.Helper()
	var tls bool
	err := f.Admin.QueryRow(ctx, `SELECT count(*), count(*) FILTER(WHERE a.wait_event_type='Lock'),coalesce(bool_and(s.ssl),true) FROM pg_stat_activity a JOIN pg_stat_ssl s USING(pid) WHERE a.usename=$1`, f.Role).Scan(&all, &blocked, &tls)
	if err != nil || !tls {
		t.Fatal("independent SQL session witness failed TLS verification")
	}
	return
}

func (f *Fixture) Rows(ctx context.Context, t *testing.T) map[string]int {
	t.Helper()
	rows, err := f.Admin.Query(ctx, "SELECT id,pid FROM notes")
	if err != nil {
		t.Fatal("read independent SQL rows")
	}
	defer rows.Close()
	result := map[string]int{}
	for rows.Next() {
		var id string
		var pid int
		if err := rows.Scan(&id, &pid); err != nil {
			t.Fatal("decode independent SQL row")
		}
		result[id] = pid
	}
	if rows.Err() != nil {
		t.Fatal("read independent SQL rows")
	}
	return result
}

func Files(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{"handler.js": Handler}
	for _, name := range []string{"package.json", "package-lock.json", "runtime/postgres-pool-runtime.js", "runtime/binding-client-runtime.js", "runtime/secret-reload-runtime.js"} {
		data, err := templates.FS.ReadFile("rest-api-postgres/" + name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(data)
	}
	return files
}

// No application lifecycle endpoints: the generated adapter owns them.
const Handler = `import pg from 'pg';
import {setTimeout as delay} from 'node:timers/promises';
import {createPostgresPool,postgresPoolConfig} from './runtime/postgres-pool-runtime.js';
const make=()=>createPostgresPool(()=>new pg.Pool({...postgresPoolConfig(process.env.DATABASE_URL),max:1,application_name:'gregale-function-'+process.pid}),{
 timeoutMs:12000,readinessQuery:{text:'SELECT id FROM notes LIMIT 1'}
});
const databases=[make(),make()];
// A test-only peer holds ACK so SQL closure is observed BEFORE capture.
if(process.env.FUNCTION_SQL_CONTROL_URL){
 const bridge=globalThis[Symbol.for('gregale.function.postgres.lifecycle.v1')];
 if(!bridge)throw new Error('function resource bridge missing');
 const barrier=async(phase,signal)=>{
   // Resource callbacks run concurrently. Observe both pool owners finishing
   // before sampling generations; never invoke their callbacks a second time.
   const wanted=phase==='checkpoint'?'checkpointed':'ready';
   // Pool diagnostics hold no SQL or credentials; stderr reaches the test log.
   const witness=()=>'function SQL '+phase+' witness pid='+process.pid+' pools='+JSON.stringify(databases.map(db=>db.diagnostics));
   const started=Date.now();
   let reported=false;
   while(databases.some(db=>db.diagnostics.state!==wanted)){
     if(databases.some(db=>['unavailable','closed'].includes(db.diagnostics.state))){
       console.error(witness()+' failed');
       throw new Error('pool witness failed');
     }
     if(!reported&&Date.now()-started>1000){reported=true;console.error(witness()+' still waiting');}
     await delay(5,undefined,{signal}).catch(error=>{console.error(witness()+' aborted');throw error;});
   }
   const response=await fetch(process.env.FUNCTION_SQL_CONTROL_URL+'/'+phase+'?pid='+process.pid+'&generations='+databases.map(db=>db.generation).join(','),{
     method:'POST',signal,headers:{authorization:'Bearer '+process.env.FUNCTION_SQL_CONTROL_TOKEN,connection:'close'}
   }).catch(error=>{console.error(witness()+' control request failed: '+(error?.cause?.code??error?.name));throw error;});
   await response.arrayBuffer();
   if(response.status!==204){console.error(witness()+' control answered '+response.status);throw new Error('lifecycle control failed');}
 };
 bridge.register({beforeCheckpoint:({signal})=>barrier('checkpoint',signal),afterRestore:({signal})=>barrier('recovery',signal)});
}
export async function handler(event,ctx){
 const parts=event.path.split('/');
 await Promise.all(databases.map(db=>db.query('SELECT 1',[],{signal:ctx.signal})));
 if(['tail','worker-crash','runner-crash'].includes(parts[1])){
   ctx.waitUntil(databases[0].query('INSERT INTO notes(id,pid) VALUES($1,$2)',[parts[2],process.pid],{write:true,signal:ctx.signal}));
   if(parts[1]==='worker-crash')setTimeout(()=>process.exit(1),200);
   if(parts[1]==='runner-crash')setTimeout(()=>process.kill(process.ppid,'SIGTERM'),200);
   return {statusCode:202,body:JSON.stringify({pid:process.pid,generations:databases.map(db=>db.generation)})};
 }
 return {statusCode:200,body:JSON.stringify({pid:process.pid,generations:databases.map(db=>db.generation)})};
}`
