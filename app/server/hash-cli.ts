// CLI: `tsx server/hash-cli.ts <password>` or `node dist/server/hash-cli.js <password>`
// Prints the scrypt line to paste into /etc/wg-admin/app.conf.
import { hashPassword } from './config.js';

const pw = process.argv[2];
if (!pw) {
  console.error('usage: hash-cli <password>');
  process.exit(2);
}
console.log(hashPassword(pw));
