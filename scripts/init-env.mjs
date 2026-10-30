import {readFileSync,writeFileSync} from 'node:fs';
import {randomBytes} from 'node:crypto';
const root=new URL('../',import.meta.url);
let text=readFileSync(new URL('.env.example',root),'utf8');
for(const key of ['DB_ADMIN_PASSWORD','CASECITE_DB_PASSWORD','GOODMEM_DB_PASSWORD','SECRET_KEY','REGISTRATION_BOOTSTRAP_TOKEN']) text=text.replace(`${key}=\n`,`${key}=${randomBytes(32).toString('hex')}\n`);
writeFileSync(new URL('.env',root),text,{flag:'wx',mode:0o600});
console.log('Created .env with unique secrets. Add your embedding/model key before starting the fresh stack.');
