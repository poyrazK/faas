/** Bounded RFC 4180 reader for the example's row,email format. */
export function csvInputs(text,importId,failRow) {
  if(Buffer.byteLength(text)>1024*1024||text.includes('\0')||!/^[A-Za-z0-9_.:-]{1,80}$/.test(importId))throw new Error('Invalid CSV import');
  const records=[];let row=[],field='',quoted=false,closed=false;
  const cell=()=>{row.push(field);field='';closed=false;};
  const record=()=>{cell();records.push(row);row=[];if(records.length>1001)throw new Error('CSV limit');};
  for(let i=0;i<text.length;i++){
    const ch=text[i];
    if(quoted){if(ch==='"'){if(text[i+1]==='"'){field+='"';i++;}else{quoted=false;closed=true;}}else field+=ch;continue;}
    if(ch==='"'){if(field||closed)throw new Error('Invalid CSV quoting');quoted=true;}
    else if(ch===',')cell();
    else if(ch==='\n'||ch==='\r'){if(ch==='\r'&&text[i+1]==='\n')i++;record();}
    else{if(closed)throw new Error('Invalid CSV quoting');field+=ch;}
  }
  if(quoted)throw new Error('Unclosed CSV field');if(field||row.length||closed)record();
  if(records.shift()?.join(',')!=='row,email'||!records.length)throw new Error('Expected row,email header');
  return records.map(fields=>{if(fields.length!==2||!/^(0|[1-9]\d*)$/.test(fields[0])||!Number.isSafeInteger(Number(fields[0])))throw new Error('Invalid CSV row');const row=Number(fields[0]);return {id:importId+':'+row,value:{row,email:fields[1],...(String(row)===String(failRow)?{fail_once:true}:{})}};});
}
