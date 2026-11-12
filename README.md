# rtm — launchd routine manager

맥의 launchd·crontab에 등록된 Claude Code·Codex 루틴을 한 화면에서 조회하고, 수동 실행하고, 일정을 바꾸는 TUI/CLI 도구.

루틴을 새로 만드는 일은 rtm 밖에서 한다. 에이전트로 만들 때는 [루틴 생성 스킬](skills/launchd-routine-create/SKILL.md)을 쓴다.

## 설치

Go 1.27 이상이 필요하다.

```sh
git clone https://github.com/HOYUN-Y/launchd-routine-manager.git
cd launchd-routine-manager
make install               # /usr/local/bin/rtm 에 설치
rtm doctor                  # claude·codex 경로, LaunchAgents 접근, launchctl 점검
```

`make install`은 빌드 후 기본 PATH에 포함된 `/usr/local/bin`에 설치하므로, 새 터미널에서도 어느 폴더에서든 `rtm`을 실행할 수 있다. 설치 폴더에 쓰기 권한이 없으면 설치 단계에서만 `sudo`로 관리자 비밀번호를 요청한다. `sudo make install`은 필요하지 않다.

업데이트는 프로젝트 폴더에서 실행한다.

```sh
git pull --ff-only
make install
```

설치 위치를 바꾸려면 `make install PREFIX="$HOME/.local"` 또는 `make install BINDIR="원하는/bin/경로"`를 쓴다. 사용자 지정 경로는 PATH에 포함되어 있어야 `rtm`만으로 실행할 수 있다.

기존 `go install ./cmd/rtm`도 사용할 수 있으나, Go 설치 경로(기본 `~/go/bin`)를 별도로 PATH에 추가해야 할 수 있다. 이전에 설치한 실행 파일이 있다면 `command -v rtm`으로 현재 선택되는 경로를 확인한다.

설치하지 않고 써 보려면 `go build -o rtm ./cmd/rtm && ./rtm`.

### 루틴 생성 스킬 설치 (선택)

Claude Code·Codex가 루틴을 만들 때 이 스킬을 따르게 하려면 링크한다.

```sh
mkdir -p ~/.claude/skills ~/.codex/skills
ln -s "$PWD/skills/launchd-routine-create" ~/.claude/skills/launchd-routine-create
ln -s "$PWD/skills/launchd-routine-create" ~/.codex/skills/launchd-routine-create
```

같은 이름의 스킬이 이미 있으면 `ln`이 실패한다. 그 경우 기존 것을 확인한 뒤 결정한다.

## 사용법

### TUI

```sh
rtm
```

| 키 | 동작 |
|---|---|
| `↑`/`↓`, `k`/`j` | 이동 |
| `enter` | 상세 보기 |
| `a` | AI 루틴만 ↔ 전체 보기 |
| `/` | 검색 |
| `ctrl+r` | 목록·상세 새로고침 (검색 조건·선택 유지) |
| `r` | 지금 실행 |
| `e` | 일정 변경 |
| `l` | 로그 (`f`: 따라가기) |
| `t` | AI 루틴으로 수동 지정/해제 |
| `esc` | 뒤로 |
| `q` | 종료 |

기본 화면은 claude·codex를 호출하는 루틴만 보여 준다. 비어 있으면 `a`로 전체를 본다.

### CLI

```
rtm list [--all] [--json]          AI 루틴 목록 (--all: 모든 plist와 crontab 항목)
rtm show <label> [--json]          일정, 다음 실행, 상태, 로그 경로, 명령
rtm run <label> [--restart] [--yes] [--load] [--wait 10s]
                                   지금 실행 (launchctl kickstart)
rtm schedule <label> --daily HH:MM | --weekly mon,wed HH:MM | --at HH:MM,HH:MM | --every 30m
             [--yes] [--force]     일정 변경 (백업 후 plist 수정·재로드)
rtm schedule <label> --revert      가장 최근 백업으로 되돌리기
rtm logs <label> [-n 50] [-f]      로그 마지막 줄
rtm track <label> / untrack <label>
                                   AI 루틴 수동 지정/해제
rtm doctor [--json]                환경 점검
```

예:

```sh
rtm run com.me.routine.changelog-update
rtm schedule com.me.routine.changelog-update --weekly mon,fri 09:30
rtm schedule com.me.routine.changelog-update --revert
```

## 동작 범위

- **조회**: `~/Library/LaunchAgents`, `/Library/LaunchAgents`(읽기 전용), 사용자 crontab
- **실행·일정 변경**: `~/Library/LaunchAgents`의 plist만. `/Library`·`/System`은 건드리지 않는다
- **일정 변경**: 백업(`~/.local/state/rtm/backups/`) → 일정 키만 수정 → `plutil -lint` → bootout/bootstrap → 반영 확인. 실패하면 원래대로 되돌린다
- **실행 중인 루틴**: 재시작·재로드 전에 확인을 받는다(`--yes`로 생략)
- **설정**: `~/.config/rtm/config.json` (수동 지정한 루틴 목록)

## 알려진 한계

- 실행 중 확인과 재로드 사이에 아주 짧은 경쟁 구간이 있다
- 외부 명령은 15초 고정 제한 시간으로 끊는다
- 여러 rtm 프로세스가 동시에 설정 파일을 쓸 때의 잠금이 없다
- 서머타임으로 존재하지 않는 시각은 다음 실행 계산에서 건너뛴다

## 개발

```sh
go test -race ./...
RTM_INTEGRATION=1 go test ./...    # 실제 launchd 통합 테스트 (dev.rtm.test.* 라벨만 사용)
```

기획은 [docs/PLAN.md](docs/PLAN.md), 진행 기록은 [docs/EXECUTION.md](docs/EXECUTION.md).
