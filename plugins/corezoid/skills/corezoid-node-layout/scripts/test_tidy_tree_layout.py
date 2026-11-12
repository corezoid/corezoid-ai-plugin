#!/usr/bin/env python3
"""Golden tests for tidy_tree_layout.py (run in CI; stdlib only).

Each case runs the script on a copy of a fixture and compares the written file byte-for-byte with
fixtures/expected/<case>.conv.json. A second pass with --only-new (nothing new to place) must leave
the result unchanged: layout is stable and the output format round-trips.

    python3 test_tidy_tree_layout.py            # run
    python3 test_tidy_tree_layout.py --update   # regenerate expected files after an intended change
"""
import os, shutil, subprocess, sys, tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, 'tidy_tree_layout.py')
FIXTURES = os.path.join(HERE, 'fixtures')
EXPECTED = os.path.join(FIXTURES, 'expected')

# (case name, fixture, flags)
CASES = [
    ('synthetic-dispatcher', 'synthetic-dispatcher.conv.json', []),
    ('synthetic-dispatcher-vertical', 'synthetic-dispatcher.conv.json', ['--vertical']),
    ('setparam-errors', 'setparam-errors.conv.json', []),
    # rule 21: only the plain string-only Set Parameters is rewired; $.math, conv[...] and an
    # object key keep their own error path
    ('setparam-errors-fixed', 'setparam-errors.conv.json', ['--fix-setparam-errors']),
]


def run(path, *flags):
    r = subprocess.run([sys.executable, SCRIPT, path, *flags], capture_output=True, text=True)
    if r.returncode != 0:
        raise SystemExit(f'{SCRIPT} {path} {" ".join(flags)} failed:\n{r.stdout}{r.stderr}')
    return r.stdout


def main():
    update = '--update' in sys.argv[1:]
    failed = []
    with tempfile.TemporaryDirectory() as tmp:
        for name, fixture, flags in CASES:
            out = os.path.join(tmp, name + '.conv.json')
            shutil.copy(os.path.join(FIXTURES, fixture), out)
            run(out, *flags)
            with open(out, 'rb') as f:
                got = f.read()
            expected_path = os.path.join(EXPECTED, name + '.conv.json')
            if update:
                os.makedirs(EXPECTED, exist_ok=True)
                with open(expected_path, 'wb') as f:
                    f.write(got)
                print(f'updated {name}')
                continue
            with open(expected_path, 'rb') as f:
                if f.read() != got:
                    failed.append(f'{name}: output differs from {os.path.relpath(expected_path, HERE)}')
                    continue
            before = os.path.join(tmp, name + '.before.json')
            shutil.copy(out, before)
            run(out, '--only-new', before)
            with open(out, 'rb') as f:
                if f.read() != got:
                    failed.append(f'{name}: a second pass (--only-new, nothing new) changed the file')
                    continue
            print(f'ok {name}')
    if failed:
        print('\n'.join(['FAIL ' + m for m in failed]))
        print('If the change is intended, run with --update and review the diff of fixtures/expected/.')
        sys.exit(1)


if __name__ == '__main__':
    main()
