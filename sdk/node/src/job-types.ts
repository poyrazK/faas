#!/usr/bin/env node
/** Generate TS and Python types from the same portable job contract. */
import { readFile, writeFile, realpath } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import { JobContract, type JobContractSpec, type JobSchema } from './job-runtime.js';
export function generateJobTypes(spec: JobContractSpec, language: 'typescript' | 'python'): string {
    if(!['typescript','python'].includes(language))throw new TypeError('Unsupported language');
    const contract = new JobContract(spec);
    spec = contract.spec;
    if (language === 'typescript') {
        const type = (s: JobSchema): string => s.enum ? s.enum.map(v => JSON.stringify(v)).join(' | ') : s.type === 'object' ? '{ ' + Object.entries(s.properties!).map(([k, v]) => 'readonly ' + JSON.stringify(k) + (s.required!.includes(k) ? '' : '?') + ': ' + type(v) + ';').join(' ') + ' }' : s.type === 'array' ? 'ReadonlyArray<' + type(s.items!) + '>' : s.type === 'integer' ? 'number' : s.type;
        return '// Generated from a Gregale job contract.\nimport {JobContract} from "@gregale/sdk-node/jobs/runtime";\nexport type Input = ' + type(spec.input) + ';\nexport type Output = ' + type(spec.output) + ';\nexport const contract = new JobContract<Input, Output>(' + JSON.stringify(spec) + ' as const);\n';
    }
    const declarations: string[] = [];
    const pyString = (v: string) => JSON.stringify(v);
    const literal = (v: string | number | boolean | null): string => v === null ? 'None' : v === true ? 'True' : v === false ? 'False' : typeof v === 'string' ? pyString(v) : String(v);
    const type = (s: JobSchema, name: string): string => {
        if (s.enum)
            return 'Literal[' + s.enum.map(literal).join(', ') + ']';
        if (s.type === 'object') {
            const fields = Object.entries(s.properties!).map(([k, v]) => { const child = type(v, name + '_' + k); return pyString(k) + ': ' + (s.required!.includes(k) ? child : 'NotRequired[' + child + ']'); });
            declarations.push(name + ' = TypedDict(' + pyString(name) + ', {' + fields.join(', ') + '})');
            return name;
        }
        if (s.type === 'array')
            return 'list[' + type(s.items!, name + '_item') + ']';
        return { integer: 'int', number: 'float', string: 'str', boolean: 'bool', null: 'None' }[s.type];
    };
    const input = type(spec.input, 'Input'), output = type(spec.output, 'Output');
    if (input !== 'Input')
        declarations.push('Input: TypeAlias = ' + input);
    if (output !== 'Output')
        declarations.push('Output: TypeAlias = ' + output);
    return '# Generated from a Gregale job contract.\n# ruff: noqa: F401\nimport json\nfrom typing import Literal, NotRequired, TypeAlias, TypedDict\nfrom faas_sdk.job_runtime import JobContract\n' + declarations.join('\n') + '\ncontract: JobContract[Input, Output] = JobContract(json.loads(' + pyString(JSON.stringify(spec)) + '))\n';
}
if (process.argv[1] && pathToFileURL(await realpath(process.argv[1]).catch(()=>'' )).href === import.meta.url) {
    try {
        const [source, language, destination] = process.argv.slice(2);
        if (!source || !destination || !['typescript', 'python'].includes(language!))
            throw new Error();
        await writeFile(destination, generateJobTypes(JSON.parse(await readFile(source, 'utf8')) as JobContractSpec, language as 'typescript' | 'python'));
    }
    catch {
        console.error('Use gregale-job-types CONTRACT.json typescript|python OUTPUT; contract must use supported schema keywords.');
        process.exitCode = 1;
    }
}
