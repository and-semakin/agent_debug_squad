// ZCode desktop 3.12.3 / runtime 0.16.5 host compatibility bridge.
// Only native bootstrap/auth lives here; Go owns sessions and run policy.
'use strict';
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const Module = require('node:module');
const {spawn} = require('node:child_process');
const readline = require('node:readline');
const supportedHash = 'da61b0663336a65f7cce3dec223678794ccaa58158e304fc0d97b695434a8f01';
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
async function main() {
  const runtime = process.argv[1];
  let source = fs.readFileSync(runtime, 'utf8');
  if (crypto.createHash('sha256').update(source).digest('hex') !== supportedHash) {
    throw Error('Unsupported ZCode runtime fingerprint; this adapter supports desktop 3.12.3 runtime 0.16.5. Revalidate/update the adapter before using this build.');
  }
  // The fingerprint guards these private native exports. Never alter the installed file.
  source = source.replace('RSe();HMs();async function HMs()', 'RSe();async function HMs()');
  source += '\nHPt();V3e();module.exports={credentials:gb,registry:q3e};';
  const native = new Module(runtime, module);
  native.filename = runtime;
  native.paths = Module._nodeModulePaths(path.dirname(runtime));
  native._compile(source, runtime);
  const home = process.env.HOME || os.homedir();
  const accountDir = path.join(home, '.zcode', 'v2');
  const cache = JSON.parse(fs.readFileSync(path.join(accountDir, 'coding-plan-cache.json'), 'utf8'));
  if (cache.entryStatus?.items?.['builtin:zai-coding-plan']?.status !== 'available') {
    throw Error('ZCode Coding Plan account unavailable; sign in and refresh the plan in ZCode.');
  }
  const names = Object.keys(JSON.parse(fs.readFileSync(path.join(accountDir, 'credentials.json'), 'utf8')))
    .filter(key => key.startsWith('account-provider:coding-plan:' + provider + ':account:') && key.endsWith(':api-key'));
  if (names.length !== 1) throw Error('Expected exactly one ZCode Z.AI Coding Plan credential; select/sign in to an unambiguous account in ZCode.');
  secret = await native.exports.credentials({env: process.env}).load(names[0]);
  if (!secret) throw Error('ZCode credential is unavailable; sign in again in ZCode.');
  const env = {...process.env};
  env.ZCODE_BUILTIN_PROVIDER_CONFIG_FILE ||= path.resolve(path.dirname(runtime), '../config/provider/zcode-builtin.json');
  env.ZCODE_PERSONAL_PROVIDER_CONFIG_FILE ||= path.join(accountDir, 'provider_config.json');
  const registry = await native.exports.registry(env);
  let revision;
  try { revision = (await registry.runtime.configService.read()).zcodeBuiltinRevision; }
  finally { registry.dispose(); }
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
module.exports = {safe, setSecretForTest(value) { secret = value; }, supportedHash};
