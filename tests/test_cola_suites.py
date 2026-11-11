import json
import subprocess
import unittest

from _comun import RAIZ

HOOK = str(RAIZ / "caja" / "hooks" / "cola-suites.sh")


def hook(cmd):
    r = subprocess.run(["bash", HOOK], input=json.dumps({"tool_input": {"command": cmd}}),
                       capture_output=True, text=True, timeout=10)
    return r.returncode, r.stderr


class ColaSuites(unittest.TestCase):
    BLOQUEA = ["npx jest", "npm test", "npm t", "npm run test:unit", "yarn test", "pnpm test",
               "jest src/a.spec.ts", "npx vitest run", "dotnet test", "cd back && npm test",
               "CI=1 npx jest a.spec.ts", "npm run lint; npx jest"]
    PASA = ["ls -la", "npm install", "npm run build", 'echo "npm test"', "grep -r 'npx jest' src",
            'git commit -m "test: jest"', "npx jest --listTests", "npx tsc --noEmit",
            "~/.claude/harness/gate-lock.sh test 2 -- npm test -- --ci --maxWorkers=2",
            "cd back && ~/.claude/harness/gate-lock.sh test auto -- npx jest --maxWorkers=2"]

    def test_bloquea_suites_sueltas(self):
        for c in self.BLOQUEA:
            with self.subTest(c=c):
                self.assertEqual(hook(c)[0], 2)

    def test_deja_pasar_lo_demas(self):
        for c in self.PASA:
            with self.subTest(c=c):
                self.assertEqual(hook(c)[0], 0)

    def test_no_se_esquiva_con_otro_grupo(self):
        self.assertEqual(hook("~/.claude/harness/gate-lock.sh otro 9 -- npx jest")[0], 2)

    def test_sugiere_el_comando_exacto(self):
        casos = {
            "npx jest": "gate-lock.sh test auto -- npx jest --maxWorkers=2",
            "npm test": "gate-lock.sh test auto -- npm test -- --maxWorkers=2",
            "npm run test:unit -- --ci": "gate-lock.sh test auto -- npm run test:unit -- --ci --maxWorkers=2",
            "npx jest --maxWorkers=4": "gate-lock.sh test auto -- npx jest --maxWorkers=4",
            "dotnet test": "gate-lock.sh test auto -- dotnet test",
            "CI=1 npx jest a.spec.ts": "gate-lock.sh test auto -- env CI=1 npx jest a.spec.ts --maxWorkers=2",
        }
        for c, sug in casos.items():
            with self.subTest(c=c):
                self.assertIn(sug, hook(c)[1])

    def test_sin_comando_no_opina(self):
        r = subprocess.run(["bash", HOOK], input="{}", capture_output=True, text=True)
        self.assertEqual(r.returncode, 0)


if __name__ == "__main__":
    unittest.main()
