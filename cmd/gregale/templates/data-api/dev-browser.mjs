import { readFile } from 'node:fs/promises'
import { join } from 'node:path'

// Share the inspector's fragment capability; no test tokens appear in URLs.
export async function browserExample(directory, state, inspectorURL) {
  const client = join(directory, 'client')
  let root = join(client, 'browser'), html
  try { html = await readFile(join(root, 'index.html'), 'utf8') }
  catch { root = client; html = await readFile(join(root, 'index.html'), 'utf8') }
  const bundle = await readFile(join(root, 'client.js'))
  const startup='<script type="module" src="session.js"></script>'
  // HTML permits omitted body end tags, as the notes starter does.
  if (/<\/body\s*>/i.test(html)) html=html.replace(/<\/body\s*>/i,startup+'</body>')
  else if (/<\/html\s*>/i.test(html)) html=html.replace(/<\/html\s*>/i,startup+'</html>')
  else html+=startup
  const bootstrap = `
const capability = location.hash.slice(1)
const response = await fetch('/__gregale/dev/session', {headers:{Authorization:'Bearer '+capability},cache:'no-store'})
if (!response.ok) throw new Error('Open the private browser link printed by Gregale.')
const session = await response.json()
const selector=document.createElement('select');selector.setAttribute('aria-label','Local test identity')
for(const name of Object.keys(session.identities)){const option=document.createElement('option');option.value=name;option.textContent=name;selector.append(option)}
const label=document.createElement('label');label.textContent='Local test identity ';label.append(selector);document.body.prepend(label)
const field=name=>document.querySelector('[name="'+name+'"]')??document.getElementById(name)
const apply=()=>{const identity=session.identities[selector.value];field('url').value=session.url;if(field('subject'))field('subject').value=identity.subject;field('token').value=identity.token}
selector.onchange=()=>{if(Array.from(document.querySelectorAll('[name=subject],[name=url],#url')).some(el=>el.disabled)) {selector.value=selector.dataset.current;return} apply();selector.dataset.current=selector.value}
selector.dataset.current=selector.value;apply()
document.addEventListener('submit',apply,true)
document.addEventListener('click',event=>{if(event.target.closest('button'))apply()},true)
`
  const assets = new Map([['', [html, 'text/html']], ['client.js', [bundle, 'text/javascript']], ['session.js', [bootstrap, 'text/javascript']]])
  return {
    url: inspectorURL.replace('/__gregale/dev/', '/__gregale/example/'),
    handle(req, res) {
      const prefix = '/__gregale/example/'
      if (!req.url.startsWith(prefix)) return false
      const origin = state().url
      const asset = assets.get(req.url.slice(prefix.length))
      if (!origin || req.headers.host !== new URL(origin).host || req.headers['sec-fetch-site'] === 'cross-site' || req.headers.origin && req.headers.origin !== origin) { res.writeHead(403);res.end();return true }
      if (req.method !== 'GET' || !asset) {res.writeHead(404);res.end();return true}
      res.writeHead(200, {'Content-Type':asset[1]+'; charset=utf-8','Cache-Control':'no-store','Referrer-Policy':'no-referrer','X-Content-Type-Options':'nosniff','Content-Security-Policy':"default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'"})
      res.end(asset[0]);return true
    },
  }
}
