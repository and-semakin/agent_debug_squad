// Opt-in compatibility research only. Never used by `models` or normal runs.
// Usage: node scripts/probe-zcode-model-bootstrap.cjs /path/to/zcode.cjs
// Uses fixture account/config paths, blocks writes, subprocesses and networking.
const fs = require('node:fs'), os = require('node:os'), path = require('node:path');
const Module = require('node:module');
const host = require('../internal/adapters/zcode/host.cjs');
const runtime = path.resolve(process.argv[2]);
const source = fs.readFileSync(runtime, 'utf8');
const found = host.discover(source); // Structural match before compilation.
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'squad-zcode-probe-'));
const remove = fs.rmSync.bind(fs);
const fixture = path.join(root, 'provider.json');
fs.writeFileSync(fixture, JSON.stringify({revision:'fixture',providers:{}}));
process.env = {HOME:root,PATH:process.env.PATH,ZCODE_DATA_BASE_DIR:root,
 ZCODE_BUILTIN_PROVIDER_CONFIG_FILE:fixture,ZCODE_PERSONAL_PROVIDER_CONFIG_FILE:fixture};
let blocked = '', stage = 'compile';
const deny = kind => () => {blocked=kind;throw Error('probe_denied');};
for (const k of ['writeFile','writeFileSync','appendFile','appendFileSync','mkdir','mkdirSync','rename','renameSync','unlink','unlinkSync','rm','rmSync','rmdir','rmdirSync','createWriteStream','truncate','truncateSync','copyFile','copyFileSync','symlink','symlinkSync']) if(fs[k]) fs[k]=deny('persistent_write');
for (const k of ['writeFile','appendFile','mkdir','rename','unlink','rm','rmdir','truncate','copyFile','symlink']) if(fs.promises[k]) fs.promises[k]=deny('persistent_write');
for (const api of [fs,fs.promises]) {
 const original=api.open.bind(api);
 api.open=(p,flags,...args)=>{if(flags!=='r' && flags!==0)return deny('persistent_write')();return original(p,flags,...args)};
}
const openSync=fs.openSync.bind(fs);fs.openSync=(p,flags,...args)=>{if(flags!=='r' && flags!==0)return deny('persistent_write')();return openSync(p,flags,...args)};
for (const name of ['node:http','node:https']) {const m=require(name);m.request=deny('network');m.get=deny('network');}
const net=require('node:net');net.connect=net.createConnection=net.Socket.prototype.connect=deny('network');
globalThis.fetch=deny('network');
const cp=require('node:child_process');for(const k of ['spawn','spawnSync','exec','execSync','execFile','execFileSync','fork'])cp[k]=deny('subprocess');
const originalLoad=Module._load;Module._load=function(name,...args){if(name.endsWith('.node') || name==='node:sqlite')return deny('native_storage')();return originalLoad.call(this,name,...args)};
const report=()=>{process.stdout.write(JSON.stringify({stage,blocked:blocked||null,verified:false})+'\n');try{remove(root,{recursive:true,force:true})}catch{}process.exit(0)};
setTimeout(()=>{blocked ||= 'bootstrap_timeout';report()},3000);
(async()=>{
 try {
  const native=new Module(runtime,module);native.filename=runtime;native.paths=Module._nodeModulePaths(path.dirname(runtime));
  native._compile(found.patched+'\n;module.exports={pick:name=>eval(name)};',runtime);
  const pick=native.exports.pick;
  stage='lazy_bootstrap';for(const thunk of found.thunks)pick(thunk)();
  stage='registry_start';const registry=await pick(found.registry)(process.env);
  try {stage='registry_view';if(typeof registry?.registry?.getView==='function')registry.registry.getView();}
  finally {registry?.dispose?.()}
 } catch { /* No raw exception, source, account data or paths in output. */ }
 report();
})();
