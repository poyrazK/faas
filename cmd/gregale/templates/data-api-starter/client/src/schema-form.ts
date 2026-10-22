import * as contract from './database.types.js'
export type FormField = Readonly<{name:string;control:'text'|'number'|'checkbox'|'select'|'custom';required:boolean;nullable:boolean;options?:readonly string[];maxLength?:number;lengthUnit?:string;min?:number;max?:number;step?:number;requiresReview:boolean}>
const metadata=(contract as unknown as {formMetadata?:{api:{notes:{insert:readonly FormField[];update:readonly FormField[]}}}}).formMetadata
const fallback:readonly FormField[]=[{name:'body',control:'text',required:true,nullable:false,requiresReview:true},{name:'priority',control:'number',required:false,nullable:false,step:1,requiresReview:true}]
/** Keep the application-owned allowlist explicit: never render subject, ID or version. */
export function noteFormFields(mode:'insert'|'update'):readonly FormField[]{
 return (metadata?.api.notes[mode]??fallback.map(field=>({...field,required:mode==='insert'&&field.required}))).filter(field=>(mode==='insert'?['body']:['body','priority']).includes(field.name))
}
export function renderNoteFields(container:HTMLElement,mode:'insert'|'update'){
 container.replaceChildren()
 for(const field of noteFormFields(mode)){
  if(field.control==='custom') continue
  const label=document.createElement('label'),input=field.control==='select'?document.createElement('select'):field.name==='body'&&field.control==='text'?document.createElement('textarea'):document.createElement('input')
  label.textContent=field.name==='body'?'Note body ':'Priority '
  input.name=field.name;input.required=field.required&&field.control!=='checkbox'
  if(input instanceof HTMLInputElement){input.type=field.control==='checkbox'?'checkbox':field.control==='number'?'number':'text';if(field.min!==undefined)input.min=String(field.min);if(field.max!==undefined)input.max=String(field.max);if(field.step!==undefined)input.step=String(field.step);else if(field.control==='number')input.step='any'}
  if(input instanceof HTMLSelectElement){
   if(!field.required){const option=document.createElement('option');option.value='';option.textContent=mode==='update'?'Keep current value':'Use database default';input.append(option)}
   for(const value of field.options??[]){const option=document.createElement('option');option.value=value;option.textContent=value;input.append(option)}
  }
  // HTML maxlength counts UTF-16 units, while PostgreSQL varchar counts code
  // points. Use metadata as guidance and the generated parser for enforcement.
  if(field.maxLength!==undefined)label.append(document.createTextNode(`(up to ${field.maxLength} characters) `))
  const error=document.createElement('span');error.id=`${mode}-${field.name}-error`;error.setAttribute('role','alert');input.setAttribute('aria-describedby',error.id)
  const paragraph=document.createElement('p');label.append(input);paragraph.append(label,error);container.append(paragraph)
 }
}
