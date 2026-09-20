// ZCode desktop 3.x host compatibility bridge.
// Only native bootstrap/auth lives here; Go owns sessions and run policy.
'use strict';
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const Module = require('node:module');
const {spawn} = require('node:child_process');
const readline = require('node:readline');
const provider = 'account:zai-individual-coding-plan';
let secret = '';
let child;
function safe(value) {
  let text = String(value);
  if (secret) {
    text = text.split(secret).join('[redacted]');
    text = text.split(JSON.stringify(secret).slice(1, -1)).join('[redacted]');
  }
  return text;
}
function emit(value) { process.stdout.write(safe(JSON.stringify(value)) + '\n'); }
function send(value) { child.stdin.write(JSON.stringify(value) + '\n'); }
function fatal(message) {
  process.stderr.write(safe(message) + '\n');
  if (child) child.kill('SIGTERM');
  process.exitCode = 1;
  process.stdin.destroy();
}
// Minified identifiers change on every ZCode rebuild, so compatibility is probed
// structurally instead of pinned to one bundle hash: the CLI autorun statement,
// the credentials.json path builder and the store factory built on it, and the
// unique *RegistryRuntime keep-name class with its single construction site.
// Every anchor must match exactly once before the bundle is loaded.
function unsupported(hash, what) {
  throw Error(`Unsupported ZCode runtime ${hash}: ${what}; this adapter supports ZCode desktop 3.x installs it can probe structurally. Update agent-debug-squad or reinstall a compatible ZCode desktop, then retry.`);
}
function discover(source) {
  const hash = crypto.createHash('sha256').update(source).digest('hex');
  const fail = what => unsupported(hash, what);
  const ident = value => /^[A-Za-z_$][\w$]*$/.test(value) ? value : fail(`unreadable identifier near ${JSON.stringify(value)}`);
  const exactlyOne = (pattern, what) => {
    const all = [...source.matchAll(pattern)];
    return all.length === 1 ? all[0] : fail(`${what} anchor: ${all.length} matches, expected exactly 1`);
  };
  const precedingFunction = (index, what) => {
    const at = source.lastIndexOf('function ', index);
    if (at < 0) return fail(`${what}: no enclosing function`);
    return ident((/^([A-Za-z_$][\w$]*)/.exec(source.slice(at + 9, index)) || [])[1]);
  };
  // 1. CLI autorun, e.g. "q9t();vtc();async function vtc()" in 3.14.0. Strip the
  // call that would start the CLI main so the compiled bundle stays inert.
  const autorun = exactlyOne(/([A-Za-z_$][\w$]*)\(\);([A-Za-z_$][\w$]*)\(\);async function \2\(\)/g, 'CLI autorun');
  const patched = source.slice(0, autorun.index) + `${autorun[1]}();async function ${autorun[2]}()` + source.slice(autorun.index + autorun[0].length);
  // 2. Credential store: the unique .zcode/v2/credentials.json path builder and
  // the factory built on it, "function PM(e={}){let t=e.env??process.env,n=fUs(e),...".
  const pathFn = precedingFunction(exactlyOne(/"\.zcode","v2","credentials\.json"/g, 'credential path').index, 'credential path builder');
  const store = exactlyOne(/function ([A-Za-z_$][\w$]*)\(([A-Za-z_$][\w$]*)=\{\}\)\{let [A-Za-z_$][\w$]*=\2\.env\?\?process\.env,[A-Za-z_$][\w$]*=([A-Za-z_$][\w$]*)\(\2\)/g, 'credential store factory');
  if (store[3] !== pathFn) fail(`credential store factory builds on ${store[3]}, not the credential path builder ${pathFn}`);
  // 3. Registry: the unique *RegistryRuntime keep-name class and its only
  // construction site, wrapped by the factory the old hash build exported.
  const runtimeClass = exactlyOne(/r\(this,"([A-Za-z_$][\w$]*RegistryRuntime)"\)/g, 'provider registry runtime class');
  const classAt = source.lastIndexOf('=class', runtimeClass.index);
  if (classAt < 0) fail('provider registry runtime class declaration');
  const cls = ident((/([A-Za-z_$][\w$]*)\s*$/.exec(source.slice(Math.max(0, classAt - 200), classAt)) || [])[1]);
  const registry = precedingFunction(exactlyOne(new RegExp(`new ${cls}\\(`, 'g'), 'provider registry construction').index, 'provider registry factory');
  // 4. Module init thunks: esbuild registers each function's keep-name inside
  // its module's init thunk, which also runs the module's dependencies. Run the
  // two owning thunks so the factories' constants and classes are defined.
  const owner = name => {
    const registration = exactlyOne(new RegExp(`r\\(${name},"`, 'g'), `keep-name registration of ${name}`);
    const thunkRe = /([A-Za-z_$][\w$]*)=([A-Za-z_$][\w$]*)\(\(\)=>\{/g;
    let m, last;
    while ((m = thunkRe.exec(source)) && m.index < registration.index) last = m;
    if (!last) fail(`${name} module init thunk`);
    return ident(last[1]);
  };
  const thunks = [...new Set([owner(store[1]), owner(registry)])];
  return {hash, patched, credentials: store[1], registry, thunks};
}
// Lazy esbuild modules define their vars only when their init thunk runs; map a
// missing name back to the thunk whose var list declares it ("var ckt,ukt=Y(()=>{").
function thunkFor(source, name) {
  const re = /var ([A-Za-z_$][\w$]*(?:\s*,\s*[A-Za-z_$][\w$]*)*)\s*,\s*([A-Za-z_$][\w$]*)\s*=\s*[A-Za-z_$][\w$]*\(\(\)=>\{/g;
  for (let m; (m = re.exec(source));) {
    if (m[1].split(',').some(v => v.trim() === name)) return m[2];
  }
  return null;
}
async function main() {
  const runtime = process.argv[1];
  const source = fs.readFileSync(runtime, 'utf8');
  const found = discover(source);
  found.patched += '\n;module.exports={__squadPick:name=>eval(name)};';
  const native = new Module(runtime, module);
  native.filename = runtime;
  native.paths = Module._nodeModulePaths(path.dirname(runtime));
  native._compile(found.patched, runtime);
  const pick = name => native.exports.__squadPick(name);
  for (const thunk of found.thunks) pick(thunk)();
  // Resolve "X is not defined"/"X is not a constructor" from lazy modules by
  // running their init thunk and retrying, bounded.
  const ran = new Set();
  const ensure = async use => {
    for (let tries = 0;; tries++) {
      try { return await use(); } catch (error) {
        const m = /^(?:ReferenceError|TypeError): ([A-Za-z_$][\w$]*) is not (?:defined|a constructor|a function)$/.exec(String(error && error.message));
        const thunk = tries < 12 && m && thunkFor(source, m[1]);
        if (!thunk || ran.has(thunk)) throw error;
        ran.add(thunk);
        pick(thunk)();
      }
    }
  };
  const home = process.env.HOME || os.homedir();
  const accountDir = path.join(home, '.zcode', 'v2');
  const cache = JSON.parse(fs.readFileSync(path.join(accountDir, 'coding-plan-cache.json'), 'utf8'));
  if (cache.entryStatus?.items?.['builtin:zai-coding-plan']?.status !== 'available') {
    throw Error('ZCode Coding Plan account unavailable; sign in and refresh the plan in ZCode.');
  }
  const names = Object.keys(JSON.parse(fs.readFileSync(path.join(accountDir, 'credentials.json'), 'utf8')))
    .filter(key => key.startsWith('account-provider:coding-plan:' + provider + ':account:') && key.endsWith(':api-key'));
  if (names.length !== 1) throw Error('Expected exactly one ZCode Z.AI Coding Plan credential; select/sign in to an unambiguous account in ZCode.');
  const env = {...process.env};
  env.ZCODE_BUILTIN_PROVIDER_CONFIG_FILE ||= path.resolve(path.dirname(runtime), '../config/provider/zcode-builtin.json');
  env.ZCODE_PERSONAL_PROVIDER_CONFIG_FILE ||= path.join(accountDir, 'provider_config.json');
  // Registry entry point first: interface-checked and disposed after the
  // builtin revision is read; the credential store is interface-checked before
  // any credential value is read.
  const registry = await ensure(() => pick(found.registry)(env));
  if (typeof registry?.runtime?.configService?.read !== 'function' || typeof registry.dispose !== 'function') {
    throw Error('ZCode provider registry interface unavailable; update the adapter.');
  }
  let revision;
  try { revision = (await registry.runtime.configService.read()).zcodeBuiltinRevision; }
  finally { registry.dispose(); }
  const store = await ensure(() => pick(found.credentials)({env: process.env}));
  if (typeof store?.load !== 'function') throw Error('ZCode credential store interface unavailable; update the adapter.');
  secret = await ensure(() => store.load(names[0]));
  if (!secret) throw Error('ZCode credential is unavailable; sign in again in ZCode.');
  child = spawn(process.execPath, [runtime, 'app-server'], {env, stdio: ['pipe', 'pipe', 'pipe']});
  child.on('error', () => fatal('Could not start ZCode App Server.'));
  child.stdin.on('error', () => fatal('ZCode App Server input closed.'));
  child.on('exit', code => { process.exitCode = code || 0; process.stdin.destroy(); });
  readline.createInterface({input: child.stderr}).on('line', line => process.stderr.write(safe(line) + '\n'));
  readline.createInterface({input: child.stdout}).on('line', line => {
    if (line.length > 8 * 1024 * 1024) return fatal('ZCode protocol frame exceeds 8 MiB.');
    let msg;
    try { msg = JSON.parse(line); } catch { return fatal('Malformed ZCode App Server JSON.'); }
    if (msg.id !== undefined && msg.method === 'session/requestRuntimePreferences') {
      send({id: msg.id, result: {nativeSearchEnhancementsEnabled: false, memoryEnabled: false, askUserQuestionAutoResolutionEnabled: false}});
    } else if (msg.id !== undefined && msg.method === 'interaction/requestProviderRuntimeHeaders') {
      const ok = msg.params?.providerId === provider && msg.params?.reason === 'model-request';
      send({id: msg.id, result: ok ? {headersApplied: true, requestAuth: {apiKey: secret}} :
        {headersApplied: false, errorMessage: 'Unsupported provider or authentication challenge; resolve it in ZCode.'}});
    } else emit(msg);
  });
  readline.createInterface({input: process.stdin}).on('line', line => {
    let msg;
    try { msg = JSON.parse(line); } catch { return fatal('Malformed Squad host request.'); }
    if (msg.method === 'squad/bootstrap') {
      send({id: msg.id, method: 'provider/updateAccountConfig', params: {
        revision: 'squad-' + Date.now(), basedOnZCodeBuiltinRevision: revision,
        providers: {[provider]: {access: {type: 'zhipu-account', entitled: true}}},
        states: {[provider]: {availability: 'available', entitled: true, current: true}}
      }});
    } else send(msg);
  }).on('close', () => child.stdin.end());
}
if (!module.parent) main().catch(error => fatal(error.message));
module.exports = {safe, setSecretForTest(value) { secret = value; }, discover};
