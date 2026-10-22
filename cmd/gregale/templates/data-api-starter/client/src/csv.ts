export function parseCsv(source:string):string[][] {
  if(new TextEncoder().encode(source).length>1048576)throw new Error('CSV exceeds 1 MiB.')
  source=source.replace(/^\uFEFF/,'')
  const rows:string[][]=[];let row:string[]=[],field='',quoted=false,closed=false
  const cell=()=>{row.push(field);field='';closed=false}
  const record=()=>{cell();rows.push(row);row=[];if(rows.length>1001)throw new Error('CSV exceeds 1,000 data records.')}
  for(let i=0;i<source.length;i++){
    const c=source[i]
    if(quoted){if(c==='"'){if(source[i+1]==='"'){field+='"';i++}else{quoted=false;closed=true}}else field+=c;continue}
    if(c===','){cell();continue}
    if(c==='\r'||c==='\n'){if(c==='\r'&&source[i+1]==='\n')i++;record();continue}
    if(closed)throw new Error('Unexpected text after a quoted CSV field.')
    if(c==='"'){if(field)throw new Error('Quote inside an unquoted CSV field.');quoted=true}else field+=c
  }
  if(quoted)throw new Error('Unclosed quoted CSV field.')
  if(field||closed||row.length)record()
  if(!rows.length)throw new Error('CSV is empty.')
  rows[0]=rows[0].map(v=>v.trim())
  if(rows[0].some(v=>!v)||new Set(rows[0]).size!==rows[0].length)throw new Error('Use distinct, nonempty column headers.')
  return rows
}
