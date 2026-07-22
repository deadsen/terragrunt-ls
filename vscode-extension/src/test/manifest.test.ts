import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';

const manifest = JSON.parse(
	fs.readFileSync(path.join(__dirname, '..', '..', 'package.json'), 'utf8'),
);

test('claims only canonical Terragrunt filenames', () => {
	assert.deepEqual(manifest.contributes.languages[0].filenames, [
		'terragrunt.hcl',
		'root.hcl',
		'terragrunt.stack.hcl',
		'terragrunt.values.hcl',
	]);
});

test('activates for the Terragrunt language', () => {
	assert.ok(manifest.activationEvents.includes('onLanguage:terragrunt'));
});
