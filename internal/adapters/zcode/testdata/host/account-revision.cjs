// Synthetic native bundle: no network, real credentials, or model calls.
// Preserve the structural anchors used by discover(), but enforce the native
// 3.14.4 / 3.15.3 account DTO casing independently of the Squad host.
const r = (value, name) => value;
const Y = fn => fn;
const join = require('node:path').join;
const home = e => (e.env || process.env).HOME;
function credPath(e={}){return join(home(e),".zcode","v2","credentials.json")}
function storeFactory(e={}){let t=e.env??process.env,n=credPath(e);return {load:async()=> 'fixture-secret'}}
var storeInit=Y(()=>{r(storeFactory,"createSharedZCodeCredentialStore")});
var RC=class{static{r(this,"NodeProviderRegistryRuntime")}
  constructor(env) {
    let snapshot;
    this.runtime = {
      configService: {read: async () => ({zcodeBuiltinRevision: 'native-fixture-revision'})},
      registryService: {getView: () => {
        if (!snapshot) throw Error('account snapshot was not applied');
        return {providers: [{providerId: 'account:zai-individual-coding-plan', models: [
          {modelId: 'GLM-5.3-Flash', config: {optionSpecs: {reasoningLevel: {values: ['low', 'high']}}}}
        ]}]};
      }}
    };
    this.syncAccountProviderConfig = async value => {
      if (env.SQUAD_FIXTURE_REJECT === 'registry') throw Error('private-native-detail fixture-secret');
      snapshot = value;
    };
    this.dispose = () => {};
  }
};
function registryFactory(env){return new RC(env)}
var registryInit=Y(()=>{r(registryFactory,"startProcessProviderRegistryRuntime")});
function parseSnapshot(e) {
  const revision = e.basedOnZCodeBuiltinRevision.trim();
  if (revision !== 'native-fixture-revision' || 'basedOnZcodeBuiltinRevision' in e) throw Error('invalid native revision');
  const provider = 'account:zai-individual-coding-plan';
  if (!e.revision || !e.providers[provider].access.entitled || !e.states[provider].current) throw Error('invalid account evidence');
  return {revision: e.revision, basedOnZCodeBuiltinRevision: revision};
}
var parserInit=Y(()=>{r(parseSnapshot,"parseProcessAccountProviderConfigSnapshot")});
function init() {}
init();cli();async function cli(){
  require('node:readline').createInterface({input:process.stdin}).on('line', line => {
    const msg = JSON.parse(line);
    try {
      if (msg.method !== 'provider/updateAccountConfig') throw Error('unexpected method');
      const snapshot = parseSnapshot(msg.params);
      if (process.env.SQUAD_FIXTURE_REJECT === 'overlay') throw Error('fixture overlay rejected');
      process.stdout.write(JSON.stringify({id:msg.id,result:{status:'received',basedOnZCodeBuiltinRevision:snapshot.basedOnZCodeBuiltinRevision}})+'\n');
    } catch (error) {
      process.stdout.write(JSON.stringify({id:msg.id,error:{code:-32602,message:error.message}})+'\n');
    }
  });
}
