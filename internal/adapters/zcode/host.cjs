// ZCode desktop 3.x host compatibility bridge.
// Native bootstrap/auth and the narrow account reads live here; Go owns
// sessions, run policy, routing, and the runtime preferences answers.
'use strict';
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const Module = require('node:module');
const {spawn} = require('node:child_process');
const readline = require('node:readline');
const individualProvider = 'account:zai-individual-coding-plan';
const startProvider = 'account:zai-start-plan';
// Every secret seen during process lifetime is redacted forever, including
// rotated values loaded after the first.
const secrets = [];
let child;
function setSecret(value) {
  if (value && !secrets.includes(value)) secrets.push(value);
}
function safe(value) {
  let text = String(value);
  for (const secret of secrets) {
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
// Probe-only entry: verify the bundle's required structures and the built-in
// provider configuration exist and are readable, then stop. Never compiles
// the bundle, runs thunks, reads account data, touches native credentials,
// or spawns app-server. Emits one JSON line {ok:true, hash} on stdout.
function probe(runtime, providerArg) {
  const source = fs.readFileSync(runtime, 'utf8');
  const found = discover(source);
  // The inspector receives the resolved configuration path as an argument; a
  // missing argument falls back to the bundle-relative default.
  const providerConfig = providerArg && providerArg.trim() !== ''
    ? providerArg
    : path.resolve(path.dirname(runtime), '../config/provider/zcode-builtin.json');
  fs.accessSync(providerConfig, fs.constants.R_OK);
  emit({ ok: true, hash: found.hash });
  process.exit(0);
}

// The account directory honors ZCODE_DATA_BASE_DIR as the data base that
// replaces the ~/.zcode segment; data roots, session storage, and credential
// encryption identity stay distinct concepts.
function accountDir() {
  const base = process.env.ZCODE_DATA_BASE_DIR && process.env.ZCODE_DATA_BASE_DIR.trim() !== ''
    ? process.env.ZCODE_DATA_BASE_DIR
    : path.join(process.env.HOME || os.homedir(), '.zcode');
  return path.join(base, 'v2');
}

// decodeJwtPayload decodes the middle segment of a JWT without verifying the
// signature: the payload only supplies the account identity evidence for the
// cross-plan binding check, never trust.
function decodeJwtPayload(token) {
  const parts = String(token || '').split('.');
  if (parts.length !== 3 || !parts[1]) return null;
  try {
    const json = Buffer.from(parts[1].replace(/-/g, '+').replace(/_/g, '/'), 'base64').toString('utf8');
    const parsed = JSON.parse(json);
    return parsed && typeof parsed === 'object' ? parsed : null;
  } catch {
    return null;
  }
}

// jwtIdentityClaims extracts the identity candidates the binding check may
// compare against the Individual key's account segment. Expiry is honored
// when the payload carries it: an expired JWT never establishes identity.
const jwtIdentityFields = ['id', 'user_id', 'userId', 'sub', 'uid', 'account_id', 'accountId'];
function jwtIdentityClaims(payload) {
  if (!payload) return {claims: [], expired: false};
  let expired = false;
  if (typeof payload.exp === 'number' && Number.isFinite(payload.exp) && payload.exp * 1000 <= Date.now()) {
    expired = true;
  }
  const claims = [];
  for (const field of jwtIdentityFields) {
    const value = payload[field];
    if (typeof value === 'string' && value.trim() !== '') claims.push(value.trim());
    else if (typeof value === 'number' && Number.isFinite(value)) claims.push(String(value));
  }
  return {claims, expired};
}

// individualAccountSegment parses the account identity embedded in an
// Individual credential key name.
function individualAccountSegment(key) {
  const match = /^account-provider:coding-plan:account:([^:]+):account:(.+):api-key$/.exec(String(key || ''));
  return match ? match[2] : null;
}

// resolveIdentity binds the plan auth sources for one policy. Fixed requires
// exactly one Individual key. Start-first additionally requires a current
// ZCode JWT whose identity matches that Individual key; multiple stored keys
// are never evidence of the current account.
function resolveIdentity(policy, keyNames, token) {
  const individualKeys = keyNames.filter(key => individualAccountSegment(key));
  if (individualKeys.length !== 1) {
    return {ok: false, hasStart: false, identityMatch: false, reason: 'Expected exactly one ZCode Z.AI Coding Plan credential; select or sign in to an unambiguous account in ZCode.'};
  }
  const segment = individualAccountSegment(individualKeys[0]);
  if (policy !== 'start-first') {
    return {ok: true, segment, jwt: null, hasStart: false, identityMatch: false};
  }
  const {claims, expired} = jwtIdentityClaims(decodeJwtPayload(token));
  if (expired || !token) {
    return {ok: false, hasStart: false, identityMatch: false, reason: 'The ZCode Start Plan token is missing or expired; refresh the sign-in in ZCode.'};
  }
  const hasStart = Boolean(token) && !expired;
  const identityMatch = hasStart && claims.includes(segment);
  if (!identityMatch) {
    return {ok: false, hasStart, identityMatch: false, reason: 'The ZCode Start token and the Individual Coding Plan credential do not resolve to one current account; resolve the account in ZCode.'};
  }
  return {ok: true, segment, jwt: token, hasStart, identityMatch};
}

// appVersion resolves the real app version for the balance read: the explicit
// override, the desktop Info.plist, then a package.json next to the bundle.
// Without a version the read fails closed instead of guessing.
function appVersion(runtime) {
  const override = process.env.SQUAD_ZCODE_APP_VERSION;
  if (override && override.trim() !== '') return override.trim();
  const plist = path.resolve(path.dirname(runtime), '../../Info.plist');
  try {
    const text = fs.readFileSync(plist, 'utf8');
    const match = /<key>CFBundleShortVersionString<\/key>\s*<string>([^<]+)<\/string>/.exec(text);
    if (match) return match[1].trim();
  } catch {}
  const manifest = path.resolve(path.dirname(runtime), 'package.json');
  try {
    const parsed = JSON.parse(fs.readFileSync(manifest, 'utf8'));
    if (parsed && typeof parsed.version === 'string' && parsed.version.trim() !== '') return parsed.version.trim();
  } catch {}
  return null;
}

// readEnvelope is the bounded result contract for account reads: a normalized
// allowlisted payload or a typed failure. No credential or raw body ever
// reaches Go; messages are fixed templates.
async function guardedRead(url, authorization, parse) {
  if (typeof fetch !== 'function') {
    return {ok: false, kind: 'schema', message: 'The Node runtime lacks fetch; the account read is unavailable.'};
  }
  if (!authorization) {
    return {ok: false, kind: 'auth', message: 'No usable account credential for the guarded read; sign in or refresh the plan in ZCode.'};
  }
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 5000);
  try {
    const response = await fetch(url, {method: 'GET', headers: {Authorization: `Bearer ${authorization}`}, redirect: 'manual', signal: controller.signal});
    if (response.status >= 300 && response.status < 400) {
      return {ok: false, kind: 'schema', message: 'The account read redirected to an unverified origin; refusing to follow it.'};
    }
    const text = await response.text();
    if (text.length > 4 * 1024 * 1024) {
      return {ok: false, kind: 'schema', message: 'The account read response exceeded its size bound.'};
    }
    if (response.status === 401 || response.status === 403) {
      return {ok: false, kind: 'auth', message: 'The account read was rejected as unauthenticated; sign in or refresh the plan in ZCode.'};
    }
    if (response.status !== 200) {
      return {ok: false, kind: 'network', httpCode: response.status, message: 'The account read endpoint returned an error status.'};
    }
    let parsed;
    try { parsed = JSON.parse(text); } catch {
      return {ok: false, kind: 'schema', message: 'The account read response was not valid JSON.'};
    }
    const payload = parse(parsed);
    if (!payload) {
      return {ok: false, kind: 'schema', message: 'The account read response could not be projected onto the allowlisted evidence.'};
    }
    return {ok: true, payload};
  } catch (error) {
    return {ok: false, kind: 'network', message: 'The account read failed at the transport level.'};
  } finally {
    clearTimeout(timer);
  }
}

// Account reads use the pinned source-defined endpoints. Origins are fixed;
// redirects are never followed to an unverified origin.
const balanceOrigin = 'https://zcode.z.ai';
const subscriptionOrigin = 'https://api.z.ai';

function readStartBalance(runtime, jwt) {
  const version = appVersion(runtime);
  if (!version) {
    return Promise.resolve({ok: false, kind: 'schema', message: 'The ZCode app version could not be resolved; the Start balance read is unavailable.'});
  }
  const url = `${balanceOrigin}/api/v1/zcode-plan/billing/balance?app_version=${encodeURIComponent(version)}`;
  return guardedRead(url, jwt, parsed => {
    if (!parsed || typeof parsed !== 'object' || !parsed.data || typeof parsed.data !== 'object') return null;
    return {data: parsed.data};
  });
}

function readIndividualSubscription(credential) {
  const url = `${subscriptionOrigin}/api/biz/subscription/list`;
  return guardedRead(url, credential, parsed => {
    if (!parsed || typeof parsed !== 'object' || !parsed.data) return null;
    return {data: parsed.data};
  });
}

// readRegistryView projects the live registry service view onto the bounded
// selectability evidence: the native builtin revision plus, per provider, the
// exact model identities, their reasoning values when the view carries them,
// and the observed disabled reasons. Shapes the projection cannot interpret
// stay unknown instead of being guessed; the view is read through the same
// guarded registry runtime the bootstrap uses, and it is always disposed.
async function readRegistryView(pick, found, ensure, env) {
  let registry;
  try {
    registry = await ensure(() => pick(found.registry)(env));
  } catch {
    return {ok: false, kind: 'schema', message: 'The ZCode provider registry interface is unavailable; update the adapter.'};
  }
  if (!registry || typeof registry.runtime?.configService?.read !== 'function' || typeof registry.dispose !== 'function') {
    return {ok: false, kind: 'schema', message: 'ZCode provider registry interface unavailable; update the adapter.'};
  }
  try {
    const config = await registry.runtime.configService.read();
    if (!config || typeof config !== 'object' || typeof config.zcodeBuiltinRevision !== 'string' || config.zcodeBuiltinRevision.trim() === '') {
      return {ok: false, kind: 'schema', message: 'The registry view carried no native builtin revision.'};
    }
    const providers = [];
    const rawProviders = config.providers;
    const pushProvider = (providerId, entry) => {
      if (typeof providerId !== 'string' || providerId === '' || !entry || typeof entry !== 'object') return;
      const state = config.states && typeof config.states === 'object' ? config.states[providerId] : null;
      const providerUnavailable = Boolean(state && typeof state === 'object' && state.availability && state.availability !== 'available');
      const models = [];
      const rawModels = entry.models;
      const pushModel = model => {
        if (typeof model === 'string' && model.trim() !== '') {
          models.push({modelId: model.trim(), reasoningLevels: null, disabledReason: providerUnavailable ? 'provider unavailable' : null});
          return;
        }
        if (!model || typeof model !== 'object') return;
        const modelId = typeof model.id === 'string' && model.id.trim() !== '' ? model.id.trim()
          : (typeof model.modelId === 'string' && model.modelId.trim() !== '' ? model.modelId.trim() : null);
        if (!modelId) return;
        const levels = Array.isArray(model.reasoningLevels) && model.reasoningLevels.every(v => typeof v === 'string')
          ? model.reasoningLevels
          : null;
        const disabledReason = providerUnavailable ? 'provider unavailable'
          : (typeof model.disabledReason === 'string' && model.disabledReason.trim() !== '' ? model.disabledReason : null);
        models.push({modelId, reasoningLevels: levels, disabledReason});
      };
      if (Array.isArray(rawModels)) rawModels.forEach(pushModel);
      providers.push({providerId, models});
    };
    if (Array.isArray(rawProviders)) {
      for (const entry of rawProviders) {
        pushProvider(entry && entry.id, entry);
      }
    } else if (rawProviders && typeof rawProviders === 'object') {
      for (const [providerId, entry] of Object.entries(rawProviders)) {
        pushProvider(providerId, entry && typeof entry === 'object' ? {...entry, id: entry.id ?? providerId} : null);
      }
    } else {
      return {ok: false, kind: 'schema', message: 'The registry view carried no interpretable provider surface.'};
    }
    return {ok: true, payload: {revision: config.zcodeBuiltinRevision.trim(), providers}};
  } finally {
    try { registry.dispose(); } catch {}
  }
}

async function main() {
  if (process.env.SQUAD_PROBE === '1') {
    probe(process.argv[1], process.argv[2]);
    return;
  }
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

  const env = {...process.env};
  env.ZCODE_BUILTIN_PROVIDER_CONFIG_FILE ||= path.resolve(path.dirname(runtime), '../config/provider/zcode-builtin.json');
  env.ZCODE_PERSONAL_PROVIDER_CONFIG_FILE ||= path.join(accountDir(), 'provider_config.json');

  // Reverse requests to the runtime are forwarded to Squad; the outbound
  // authorization round-trip and the overlay translation keep their own
  // pending tables so responses always reach the right side.
  let generation = null;
  let workspace = null;
  let authMaterial = null;
  let revision = null;
  let nextInternalId = 1;
  const pendingAuth = new Map();
  const pendingOverlay = new Map();
  let bootstrapped = false;

  const credentialKeys = () => {
    try {
      return Object.keys(JSON.parse(fs.readFileSync(path.join(accountDir(), 'credentials.json'), 'utf8')));
    } catch {
      return [];
    }
  };

  const respondToSquad = (id, payload) => emit({id, ...payload});
  const failBootstrap = (id, message) => {
    respondToSquad(id, {result: {ok: false, kind: 'auth', message}});
  };

  const bootstrap = async msg => {
    if (bootstrapped) return failBootstrap(msg.id, 'Squad bootstrap already ran.');
    bootstrapped = true;
    const params = msg.params || {};
    generation = params.generation;
    workspace = params.workspace;
    const policy = params.planPolicy === 'start-first' ? 'start-first' : 'fixed';
    const names = credentialKeys().filter(key => key.startsWith(`account-provider:coding-plan:${individualProvider}:account:`) && key.endsWith(':api-key'));
    const store = await ensure(() => pick(found.credentials)({env: process.env}));
    if (typeof store?.load !== 'function') {
      return failBootstrap(msg.id, 'ZCode credential store interface unavailable; update the adapter.');
    }
    let jwt = null;
    if (policy === 'start-first') {
      try {
        const candidate = await ensure(() => store.load('zcodejwttoken'));
        if (candidate) { jwt = candidate; setSecret(candidate); }
      } catch {}
    }
    const identity = resolveIdentity(policy, names, jwt);
    if (!identity.ok) return failBootstrap(msg.id, identity.reason);
    let secret = null;
    try {
      secret = await ensure(() => store.load(names[0]));
    } catch {}
    if (!secret) return failBootstrap(msg.id, 'ZCode credential is unavailable; sign in again in ZCode.');
    setSecret(secret);
    // The subscription read is account-scoped; prefer the Z.AI OAuth access
    // token, then the Start JWT, then the Individual key. All stay in the
    // shim; Go only ever sees the normalized evidence.
    let subscriptionCredential = null;
    for (const key of [...names.filter(key => key.startsWith('oauth:') && key.endsWith(':access_token')), ...credentialKeys().filter(key => key.startsWith('oauth:') && key.endsWith(':access_token')), 'zcodejwttoken', names[0]]) {
      try {
        const candidate = await ensure(() => store.load(key));
        if (candidate) { subscriptionCredential = candidate; setSecret(candidate); break; }
      } catch {}
    }
    authMaterial = {individual: secret, start: identity.jwt, subscription: subscriptionCredential || secret};
    // Registry entry point first: interface-checked and disposed after the
    // builtin revision is read; the credential store was interface-checked
    // before any credential value was read.
    const registry = await ensure(() => pick(found.registry)(env));
    if (typeof registry?.runtime?.configService?.read !== 'function' || typeof registry.dispose !== 'function') {
      return failBootstrap(msg.id, 'ZCode provider registry interface unavailable; update the adapter.');
    }
    try { revision = (await registry.runtime.configService.read()).zcodeBuiltinRevision; }
    finally { registry.dispose(); }
    child = spawn(process.execPath, [runtime, 'app-server'], {env, stdio: ['pipe', 'pipe', 'pipe']});
    child.on('error', () => fatal('Could not start ZCode App Server.'));
    child.stdin.on('error', () => fatal('ZCode App Server input closed.'));
    child.on('exit', code => { process.exitCode = code || 0; process.stdin.destroy(); });
    readline.createInterface({input: child.stderr}).on('line', line => process.stderr.write(safe(line) + '\n'));
    wireRuntime();
    respondToSquad(msg.id, {result: {ok: true, revision, hasStart: identity.hasStart, hasIndividual: true, identityMatch: identity.identityMatch}});
  };

  const applyOverlay = msg => {
    const provider = msg.params && msg.params.provider;
    if (provider !== individualProvider && provider !== startProvider) {
      return respondToSquad(msg.id, {error: {code: -32602, message: 'Overlay provider is outside the allowed Z.AI account family.'}});
    }
    const entitled = Boolean(msg.params.entitled);
    const internalId = `squad-overlay-${nextInternalId++}`;
    pendingOverlay.set(internalId, msg.id);
    send({id: internalId, method: 'provider/updateAccountConfig', params: {
      revision: 'squad-' + Date.now(), basedOnZCodeBuiltinRevision: revision,
      providers: {[provider]: {access: {type: 'zhipu-account', entitled}}},
      states: {[provider]: {availability: entitled ? 'available' : 'unavailable', entitled, current: entitled}}
    }});
  };

  const requestHeaders = msg => {
    const params = msg.params || {};
    const provider = params.providerId;
    if (params.reason !== 'model-request' || (provider !== individualProvider && provider !== startProvider)) {
      return send({id: msg.id, result: {headersApplied: false, errorMessage: 'Unsupported provider or authentication challenge; resolve it in ZCode.'}});
    }
    const internalId = `squad-auth-${nextInternalId++}`;
    pendingAuth.set(internalId, {id: msg.id, provider, timer: setTimeout(() => {
      if (!pendingAuth.has(internalId)) return;
      pendingAuth.delete(internalId);
      send({id: msg.id, result: {headersApplied: false, errorMessage: 'The account authorization round-trip timed out.'}});
    }, 60000)});
    emit({id: internalId, method: 'squad/authorizeProviderHeaders', params: {
      requestId: msg.id, sessionId: params.sessionId, turnId: params.turnId,
      providerId: provider, modelId: params.modelId, workspace, generation
    }});
  };

  const wireRuntime = () => {
    readline.createInterface({input: child.stdout}).on('line', line => {
      if (line.length > 8 * 1024 * 1024) return fatal('ZCode protocol frame exceeds 8 MiB.');
      let msg;
      try { msg = JSON.parse(line); } catch { return fatal('Malformed ZCode App Server JSON.'); }
      if (msg.id !== undefined && msg.method === undefined) {
        // A response to a wrapped Squad request is translated back; everything
        // else reaches Squad verbatim.
        if (pendingOverlay.has(String(msg.id))) {
          const original = pendingOverlay.get(String(msg.id));
          pendingOverlay.delete(String(msg.id));
          const payload = msg.error ? {error: msg.error} : {result: msg.result};
          respondToSquad(original, payload);
          return;
        }
      }
      if (msg.id !== undefined && msg.method === 'interaction/requestProviderRuntimeHeaders') {
        return requestHeaders(msg);
      }
      // session/requestRuntimePreferences and every other reverse request or
      // notification reaches Squad, which owns the dispatch table.
      emit(msg);
    });
  };

  // Squad-owned account reads. They run in the shim with native credentials
  // and answer with the bounded envelope; they never reach the App Server.
  const requireBootstrap = () => {
    if (!bootstrapped || !authMaterial) {
      return {ok: false, kind: 'auth', message: 'The guarded account read ran before bootstrap completed.'};
    }
    return null;
  };
  const handleRead = async msg => {
    const missing = requireBootstrap();
    if (missing) return respondToSquad(msg.id, {result: missing});
    let envelope;
    if (msg.method === 'squad/readStartBalance') {
      envelope = await readStartBalance(runtime, authMaterial.start);
    } else if (msg.method === 'squad/readIndividualSubscription') {
      envelope = await readIndividualSubscription(authMaterial.subscription);
    } else {
      envelope = await readRegistryView(pick, found, ensure, env);
    }
    respondToSquad(msg.id, {result: envelope});
  };

  readline.createInterface({input: process.stdin}).on('line', async line => {
    let msg;
    try { msg = JSON.parse(line); } catch { return fatal('Malformed Squad host request.'); }
    if (msg.method === 'squad/bootstrap') {
      try { await bootstrap(msg); } catch (error) {
        failBootstrap(msg.id, 'ZCode account bootstrap failed; resolve the installation or account in ZCode.');
      }
      return;
    }
    if (msg.method === 'squad/applyAccountOverlay') {
      return applyOverlay(msg);
    }
    if (msg.method === 'squad/readStartBalance' || msg.method === 'squad/readIndividualSubscription' || msg.method === 'squad/readRegistryView') {
      try {
        await handleRead(msg);
      } catch (error) {
        respondToSquad(msg.id, {result: {ok: false, kind: 'schema', message: 'The guarded account read failed inside the shim.'}});
      }
      return;
    }
    if (msg.id !== undefined && msg.method === undefined) {
      // A Squad response: either the authorization verdict for a pending
      // runtime auth request, or the reply to a forwarded request.
      const key = String(msg.id);
      if (pendingAuth.has(key)) {
        const pending = pendingAuth.get(key);
        clearTimeout(pending.timer);
        pendingAuth.delete(key);
        const allow = Boolean(msg.result && msg.result.allow);
        if (!allow) {
          return send({id: pending.id, result: {headersApplied: false, errorMessage: 'The account authorization request is not bound to the active Squad run.'}});
        }
        const secretForProvider = pending.provider === startProvider ? authMaterial.start : authMaterial.individual;
        if (!secretForProvider) {
          return send({id: pending.id, result: {headersApplied: false, errorMessage: 'The selected provider credential is unavailable; resolve the account in ZCode.'}});
        }
        return send({id: pending.id, result: {headersApplied: true, requestAuth: {apiKey: secretForProvider}}});
      }
    }
    if (!child) {
      return fatal('Squad sent a runtime request before bootstrap completed.');
    }
    send(msg);
  }).on('close', () => { if (child) child.stdin.end(); });
}
if (!module.parent) main().catch(error => fatal(error.message));
module.exports = {safe, setSecretForTest(value) { secrets.length = 0; setSecret(value); }, discover, decodeJwtPayload, resolveIdentity, jwtIdentityClaims, individualAccountSegment};
