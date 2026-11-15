import re
import subprocess


def choose(base, tags, sha):
    pattern = re.compile(r'^v' + re.escape(base) + r'-([1-9][0-9]*)$')
    valid = [(int(match[1]), tag, commit) for tag, commit in tags if (match := pattern.fullmatch(tag))]
    existing = [row for row in valid if row[2] == sha]
    if existing:
        return max(existing)[1]
    return f'v{base}-{max((row[0] for row in valid), default=0) + 1}'


if __name__ == '__main__':
    from pathlib import Path
    base = re.search(r'var Version = "([0-9]+\.[0-9]+\.[0-9]+)"', Path('internal/version/version.go').read_text())[1]
    sha = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
    names = subprocess.check_output(['git', 'tag', '--list'], text=True).splitlines()
    tags = [(name, subprocess.check_output(['git', 'rev-list', '-n', '1', name], text=True).strip()) for name in names]
    tag = choose(base, tags, sha)
    print(f'tag={tag}')
    print(f'version={tag[1:]}')
