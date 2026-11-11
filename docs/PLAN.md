# launchd Routine Manager 기획

작성일: 2026-11-12

## 1. 목적

맥에서 Claude Code·Codex의 루틴(스킬이나 프롬프트를 정해진 시각에 실행하는 작업)을 한 화면에서 보고, 상태를 확인하고, 바로 수동 실행하고, 일정을 바꾸는 터미널 도구(TUI + CLI). 특정 컴퓨터에 묶이지 않고 어느 맥에 설치해도 그 맥의 루틴을 찾아 동작한다.

명령 이름(가칭): `rtm`

해결할 문제:

- 루틴이 launchd plist, crontab, 셸 스크립트로 흩어져 있어서 무엇이 언제 도는지 한눈에 보이지 않는다.
- 실행이 성공했는지 확인하려면 `launchctl`을 치고 로그를 찾아 열어야 한다.
- 예약 실행이 실패해도 다음에 결과물을 볼 때까지 알 수 없다.

### 역할 경계

| 누가 | 무엇을 |
|---|---|
| 에이전트(Claude·Codex) 또는 직접 코딩 | 루틴의 실제 동작(스킬, 프롬프트, 스크립트)을 만들고 launchd에 등록한다. 에이전트에게는 루틴 생성 스킬로 안내한다(16장) |
| rtm | 이미 등록된 루틴을 조회하고, 수동 실행하고, 일정을 바꾼다. 루틴 내용은 만들거나 고치지 않는다 |

### 왜 앱이 아니라 TUI인가

- 모든 기능이 `launchctl`·`plutil`·`crontab` 호출이라 GUI가 필수가 아니다
- 단일 바이너리라 가볍고, 다른 맥에 설치할 때 서명·공증(유료 Apple Developer 계정)이 필요 없다
- CLI에 `--json` 출력을 두면 나중에 메뉴바 앱이나 Claude·Codex 에이전트가 그대로 호출할 수 있다. 메뉴바 앱은 13장에서 나중 단계로 다룬다

## 2. 범위

### MVP (1단계)

| 기능 | 설명 |
|---|---|
| 루틴 탐색 | `~/Library/LaunchAgents`, `/Library/LaunchAgents`의 plist와 `crontab -l`을 스캔하고 claude·codex를 호출하는 항목을 분류한다 |
| 상태 표시 | 실행 중 여부, 마지막 종료 코드, 로드 여부, 다음 실행 예정 시각 |
| 로그 보기 | `StandardOutPath`·`StandardErrorPath`의 마지막 N줄 |
| 수동 실행 | `launchctl kickstart`로 지금 실행한다. 실행 중이면 재시작 여부를 묻는다 |
| 스케줄 변경 | `~/Library/LaunchAgents`의 루틴만 일정을 수정하고 다시 로드한다(6장) |
| 다른 맥 지원 | 경로·사용자·실행 파일 위치를 코드에 박지 않고 설치된 맥에서 찾는다(10장) |
| 수동 추적 지정 | 자동 분류가 놓친 항목을 사용자가 "추적"으로 체크한다 |

### 2단계

- 실행 이력 수집기와 실패 알림(7장)
- 정밀 추적: 사용자가 동의한 루틴만 기록용 래퍼로 감싸서 시작·종료 시각과 종료 코드를 남긴다(8장)
- 스킬 목록: `~/.claude/skills`, `~/.codex/skills`, 플러그인 스킬
- 루틴 생성 스킬 작성(16장). rtm 기능이 아니라 별도 산출물이다

### 3단계 (조사 필요)

- Claude Code 클라우드 루틴(`/schedule`로 등록한 것) 조회. 로컬 파일이 없어서 공식 API나 CLI 경로부터 확인해야 한다
- Codex 앱 자동화 조회. 저장 위치를 아직 확인하지 못했다. `~/.codex`의 sqlite에는 자동화 관련 테이블이 없다
- 메뉴바 앱(13장)

### 하지 않는 것

- 루틴을 새로 만들거나 등록하지 않는다. 루틴 내용과 plist 생성은 에이전트나 직접 코딩으로 하고, rtm은 그 이후의 조회·실행·일정 변경만 맡는다
- launchd 자체를 대체하는 스케줄러를 만들지 않는다. 실행은 언제나 launchd가 맡는다
- 시스템 데몬(`/Library/LaunchDaemons`)과 `/Library/LaunchAgents`는 수정하지 않는다. 보기만 한다
- 사용자 동의 없이 기존 plist를 바꾸지 않는다
- 맥 간 동기화나 원격 관리는 하지 않는다(12장 미해결 질문 5)

## 3. 데이터 출처와 얻을 수 있는 정보

| 정보 | 출처 | 비고 |
|---|---|---|
| 일정 | plist `StartCalendarInterval`, `StartInterval`, `RunAtLoad`, `KeepAlive` | 다음 실행 시각은 launchd가 주지 않아서 직접 계산한다 |
| 실행 명령 | plist `Program`, `ProgramArguments` | 셸 스크립트로 감싼 경우 스크립트 내용까지 확인한다 |
| 로드 여부 | `launchctl print gui/<uid>/<label>` 성공 여부 | 파일은 있는데 로드 안 된 상태를 따로 표시한다 |
| 실행 상태, PID | `launchctl print`의 `state`, `pid` | |
| 마지막 종료 코드 | `launchctl print`의 `last exit code` | 이력은 없고 직전 값 하나뿐이다 |
| 실행 횟수 | `launchctl print`의 `runs` | 로그인 이후 누적값 |
| 로그 | `stdout path`, `stderr path` | 마지막 실행 시각은 로그 파일 수정 시각으로 추정한다 |
| crontab 항목 | `crontab -l` | 상태 정보가 없다. `>> log` 리다이렉트가 있으면 그 로그만 읽는다 |

### AI 루틴 분류 규칙

1. `ProgramArguments`에 `claude`나 `codex` 실행 파일이 있으면 AI 루틴이다
2. 첫 인자가 `.sh`·`.zsh`·`.py` 등 스크립트면 파일을 열어 `claude -p`, `claude --print`, `codex exec`를 찾는다
3. 1·2에 걸리지 않아도 사용자가 수동으로 추적을 지정할 수 있다
4. 분류 결과에 근거(어떤 줄에서 찾았는지)를 같이 보여준다

## 4. 인터페이스

### TUI (`rtm`)

인자 없이 실행하면 TUI가 열린다.

```
 rtm — routines on this Mac                       [a] AI만 / 전체
┌────────────────────────┬──────────────┬────────┬───────────────┐
│ LABEL                  │ 일정         │ 상태   │ 다음 실행     │
├────────────────────────┼──────────────┼────────┼───────────────┤
│ changelog-update       │ 매일 09:00   │ ✓ 0    │ 내일 09:00    │
│ blog-collect           │ 매시간       │ ✗ 1    │ 14:00         │
│ workstation-agent      │ RunAtLoad    │ 미로드 │ -             │
└────────────────────────┴──────────────┴────────┴───────────────┘
 enter 상세  r 지금 실행  e 일정 변경  l 로그  t 추적 지정  / 검색  q 종료
```

- 상세 화면: 일정, 명령 전문, 분류 근거, 최근 로그, 이력, plist 경로
- 위험한 동작(실행 중인 루틴 재시작, 일정 변경으로 인한 재로드)은 확인 대화상자를 띄운다

### CLI 서브커맨드

TUI의 모든 동작은 서브커맨드로도 실행할 수 있다. 각 명령은 `--json`을 지원한다.

```
rtm list [--all] [--json]          # 루틴 목록과 상태
rtm show <label> [--json]          # 상세
rtm logs <label> [-n 50] [-f]      # 로그
rtm run <label> [--restart]        # 수동 실행
rtm schedule <label> --daily 09:00 # 일정 변경 (--weekly mon,wed 09:00 / --every 30m / --at 09:00,18:00)
rtm schedule <label> --revert      # 직전 백업으로 되돌리기
rtm track <label> / untrack <label>
rtm collect                        # 이력 수집 1회 (2단계)
rtm doctor                         # 환경 점검: claude·codex 경로, 권한, launchd 접근
```

## 5. 수동 실행 동작

```
launchctl kickstart gui/<uid>/<label>      # 기본
launchctl kickstart -k gui/<uid>/<label>   # 실행 중일 때 재시작 (--restart)
```

- launchd로 실행하므로 예약 실행과 같은 환경에서 돈다. 수동 실행이 성공하면 예약 실행도 성공한다고 볼 수 있다
- 로드되지 않은 루틴은 먼저 등록할지 묻는다(`launchctl bootstrap gui/<uid> <plist>`)
- 실행 중이면 `--restart` 없이는 아무것도 하지 않고 "실행 중"이라고 알린다
- `KeepAlive` 항목(상시 서비스)은 재시작 전에 한 번 더 확인을 받는다
- 실행 후 상태를 몇 초간 확인해 종료 코드와 로그를 갱신한다

## 6. 스케줄 변경

- 대상: `~/Library/LaunchAgents`에 있는 plist만 수정한다. `/Library` 쪽은 관리자 권한이 필요하므로 읽기 전용으로 표시한다
- 편집 가능한 일정 형식:
  - 매일 HH:MM
  - 요일 지정 + HH:MM
  - 여러 시각(`StartCalendarInterval` 배열)
  - N분마다(`StartInterval`)
- 위 형식으로 표현할 수 없는 기존 값(월·일 조합 등)은 원문을 보여주고 읽기 전용으로 둔다
- 적용 순서:
  1. 원본 plist를 `~/.local/state/rtm/backups/<label>-<시각>.plist`로 백업한다
  2. 일정 키만 바꿔서 쓴다. 다른 키는 그대로 둔다
  3. `plutil -lint`로 검증하고, 실패하면 백업으로 되돌린다
  4. `launchctl bootout gui/<uid>/<label>` 후 `launchctl bootstrap gui/<uid> <plist>`로 다시 로드한다(launchd는 파일 변경을 자동으로 다시 읽지 않는다)
  5. 다시 읽어서 변경값이 반영됐는지 확인한다
- 실행 중인 루틴은 재로드하면 중단되므로 변경 전에 확인을 받는다
- `--revert`로 가장 최근 백업을 복원한다
- 다른 프로그램이 관리하는 plist(설치 프로그램이 다시 쓰는 경우)는 변경이 덮어써질 수 있다. 다음 스캔 때 값이 바뀌어 있으면 "외부에서 변경됨"을 표시한다

## 7. 실행 이력과 실패 알림 (2단계)

TUI는 열려 있을 때만 돌기 때문에 이력은 별도 수집기가 맡는다.

- `rtm collect`가 한 번 실행될 때마다 추적 대상 루틴의 상태를 읽고, 이전 값과 달라졌으면 기록한다
- 이 수집기 자체를 launchd 에이전트(`rtm.collector`, 기본 5분 간격)로 등록한다. `rtm collector install` / `uninstall`
- 실패(종료 코드가 0이 아님)를 새로 감지하면 `osascript -e 'display notification …'`으로 macOS 알림을 보낸다
- 저장 위치: `~/.local/state/rtm/history.sqlite`
- 기록 항목: label, 감지 시각, state, pid, last exit code, runs, 로그 크기
- 한계: 수집 간격 사이에 여러 번 실행되면 `runs` 증가량과 마지막 종료 코드만 남는다. 정확한 기록은 8장 정밀 추적으로 해결한다
- 설치 이전 이력은 기존 로그 파일에 남은 만큼만 볼 수 있다

## 8. 정밀 추적 (2단계)

- 사용자가 루틴별로 켠다(`rtm track <label> --precise`)
- 원본 plist를 백업한 뒤 `ProgramArguments` 앞에 기록용 래퍼(`rtm wrap --label <label> -- <원래 명령>`)를 끼운다
- 래퍼가 하는 일: 시작 시각 기록, 원래 명령 실행, 종료 시각과 종료 코드 기록, 원래 종료 코드 그대로 반환
- 끄면 백업으로 복원하고 다시 로드한다

## 9. 기술 구성

- 언어: Go
  - 단일 정적 바이너리로 나와서 다른 맥에 런타임 설치가 필요 없다
  - arm64·amd64 크로스 빌드가 쉽다
- TUI: Bubble Tea + Lip Gloss
- plist 읽기·쓰기: `howett.net/plist`. 쓰기 후 `plutil -lint`로 한 번 더 검증한다
- 외부 명령: `launchctl`, `plutil`, `crontab`을 `os/exec`로 호출한다
- 이력 저장: SQLite(cgo가 필요 없는 `modernc.org/sqlite`)
- 설정·상태 경로: 설정은 `~/.config/rtm/config.json`(표준 라이브러리로 읽고 쓰며, 모르는 키는 보존), 이력과 백업은 `~/.local/state/rtm/`

대안으로 Python + Textual도 검토했다. 개발은 빠르지만 맥마다 파이썬 환경이 필요해서 배포 조건에 맞지 않는다.

## 10. 다른 맥에서 동작하기

- 사용자·경로를 코드에 박지 않는다: uid는 `os.Getuid()`, 홈은 `os.UserHomeDir()`로 실행할 때 얻는다
- `claude`·`codex` 위치는 맥마다 다르다(Homebrew `/opt/homebrew/bin`, `/usr/local/bin`, npm 전역, `~/.local/bin` 등). 그래서 로그인 셸(`zsh -lc 'command -v claude'`)로 찾고 설정 파일에서 직접 지정할 수도 있게 한다
- 둘 중 하나만 설치됐거나 둘 다 없어도 동작한다. 루틴 목록과 상태 보기는 실행 파일과 무관하다
- 스킬 폴더가 없으면 빈 목록으로 처리한다
- 최소 macOS 13. launchd 동작이 맥OS 버전마다 다른 부분은 `rtm doctor`에서 점검한다
- 배포 방법:
  - Homebrew tap(`brew install <tap>/rtm`): 권장 방식이다. 업데이트도 brew로 된다
  - GitHub Releases의 arm64·amd64 바이너리를 `curl`로 받기. `curl`로 받은 파일에는 격리 속성(quarantine)이 붙지 않아서 Gatekeeper에 막히지 않는다. 브라우저로 받은 경우에는 `xattr -d com.apple.quarantine`이 필요하다
  - `go install`
- 설정·이력·백업은 맥마다 따로 저장한다. 맥끼리 동기화하지 않는다

## 11. 위험과 대응

| 위험 | 대응 |
|---|---|
| launchd 환경에서 `claude`·`codex`를 못 찾는다(PATH 최소화) | 상세 화면과 `rtm doctor`에서 "명령을 찾을 수 없음"을 경고한다. 루틴 생성 스킬에서 절대경로나 `EnvironmentVariables` PATH를 권장한다 |
| 비대화형 실행이라 권한 확인에서 진행하지 못한다 | 로그에 권한 관련 오류가 보이면 상세 화면에 표시한다. 루틴 생성 스킬에서 `claude -p --allowedTools …`, `codex exec` 승인 옵션을 권장한다 |
| 루틴이 생성 스킬의 권장 사항을 다 지키지 못한다 | 권장 사항은 필수가 아니다. rtm은 미준수 루틴도 정보가 줄어든 채로 동작한다(16장 표) |
| 맥이 잠자기 상태라 예약 실행을 놓친다 | 깨어난 뒤 한 번만 실행된다는 것을 상세 화면에 표시한다 |
| 셸 스크립트로 감싼 루틴을 놓친다 | 수동 추적 지정과 분류 근거 표시로 보완한다 |
| 상시 실행 서비스(KeepAlive)를 실수로 재시작한다 | 재시작 전에 확인을 받는다 |
| 기존 plist를 손상시킨다 | 스케줄 변경과 정밀 추적 때만 plist를 쓴다. 쓰기 전에 반드시 백업하고, `plutil -lint` 실패 시 자동 복원한다 |
| 스케줄 재로드 중 실행 중이던 작업이 끊긴다 | 실행 중이면 변경 전에 확인을 받는다 |
| 다른 맥에서 실행 파일 경로가 다르다 | 로그인 셸로 찾고, 못 찾으면 설정 파일에서 직접 지정하게 한다 |
| TUI에는 상시 표시가 없어서 실패를 늦게 안다 | 2단계 수집기의 macOS 알림으로 보완한다 |

## 12. 미해결 질문

1. Claude Code 클라우드 루틴 목록을 CLI나 API로 가져올 수 있는가
2. Codex 앱 자동화는 어디에 저장되고, 외부에서 조회할 수 있는가
3. 실행 이력 저장 기간은 얼마로 할 것인가(기본안: 90일)
4. ~~새 루틴 만들기를 MVP에 넣을 것인가~~ → 결정: rtm에 넣지 않고 루틴 생성 스킬로 분리한다(16장)
5. 원격 관리가 필요한가. 필요하면 각 맥에 수집기와 통신 경로(같은 네트워크 또는 Tailscale 등), 인증이 추가로 필요해서 별도 단계로 설계한다. 가장 단순한 안은 `ssh <맥> rtm list --json`이다
6. 명령 이름 `rtm`이 기존 도구와 겹치는지 확인

## 13. 나중 단계: 메뉴바 앱

- TUI가 안정된 뒤 필요하면 만든다
- 로직을 새로 짜지 않고 `rtm … --json`을 호출하는 얇은 SwiftUI `MenuBarExtra` 앱으로 만든다
- 이때 추가되는 것: 메뉴바 상태 아이콘, 클릭 한 번으로 보기·실행
- 다른 맥에 배포하려면 앱은 Developer ID 서명과 공증이 필요하다

## 14. 일정 (안)

| 단계 | 내용 | 완료 기준 |
|---|---|---|
| M1 | plist·crontab 스캔, 분류, `rtm list` | 테스트용 plist 3종(claude 직접, 스크립트 래핑, 무관한 항목)이 맞게 분류된다 |
| M2 | 상태·로그·다음 실행 시각, `show`·`logs` | 로드된 항목, 로드 안 된 항목, 실행 중 항목이 각각 맞게 표시된다 |
| M3 | 수동 실행 `run` | 테스트 루틴 kickstart 후 종료 코드와 로그가 갱신된다 |
| M4 | 스케줄 변경 `schedule` | 테스트 루틴의 일정을 바꾸면 백업이 생기고, 재로드 후 새 일정으로 실행되며, `--revert`로 원복된다 |
| M5 | TUI | M1~M4 기능을 TUI에서 모두 쓸 수 있다 |
| M6 | 다른 맥 배포 | Homebrew tap 또는 Releases 바이너리로 claude·codex 경로가 다른 두 번째 맥(또는 새 사용자 계정)에서 설치·탐색·실행까지 동작한다 |
| M7 | 이력 수집기, 실패 알림 | 일부러 실패시킨 루틴이 알림으로 뜨고 이력에 남는다 |

CLI를 TUI보다 먼저 만든다. 각 기능을 스크립트로 테스트할 수 있고, TUI는 CLI가 쓰는 같은 내부 함수 위에 화면만 얹는다.

## 15. 검증 방법

- 실제 루틴 대신 테스트용 LaunchAgent를 만들어 확인한다(예: `echo` 후 `exit 0` 또는 `exit 1`)
- 기존 상시 서비스(`ai.hermes.gateway` 등)로는 수동 실행과 스케줄 변경을 테스트하지 않는다
- 다음 실행 시각 계산은 `StartCalendarInterval`의 조합(매일, 요일 지정, 여러 시각 배열)별로 단위 테스트한다
- plist 쓰기는 "읽기 → 일정만 변경 → 쓰기 → 다시 읽기"에서 일정 외 키가 그대로인지 테스트한다
- 권장 사항을 지키지 않은 plist(로그 경로 없음, 상대경로 명령, 스크립트 래핑)로도 목록·상세·실행이 깨지지 않는지 테스트한다

## 16. 루틴 생성 스킬 (rtm과 별도)

루틴을 만드는 일은 rtm 밖에서 한다. 에이전트에게 맡길 때는 규칙을 매번 프롬프트로 설명하지 않고 스킬로 전달한다. 스킬은 필요한 순간에만 불러와지고, Claude와 Codex가 같은 내용을 쓸 수 있다.

- 이름(가칭): `launchd-routine-create`
- 위치: `~/.claude/skills/`와 `~/.codex/skills/`에 같은 내용으로 둔다. 원본은 이 저장소의 `skills/launchd-routine-create/`에 두고 복사하거나 링크한다
- 스킬이 하는 일:
  1. 루틴 동작(스킬 호출, 프롬프트, 스크립트)을 사용자와 정한다
  2. 실행 스크립트와 plist를 만든다
  3. `plutil -lint`로 검증하고 `launchctl bootstrap`으로 등록한다
  4. `rtm run <label>`(rtm이 없으면 `launchctl kickstart`)으로 한 번 실행해 보고, `rtm show <label>`로 결과를 확인한다

### 권장 사항 (필수 아님)

모든 루틴이 다 지킬 수는 없다. 그래서 스킬은 지킬 수 있는 것만 적용하고, 못 지킨 항목은 사용자에게 알린다. rtm은 어느 경우에도 동작한다.

| 권장 사항 | 지키지 않았을 때 rtm에서 |
|---|---|
| plist를 `~/Library/LaunchAgents`에 둔다 | 다른 위치는 탐색 대상이 아니라 rtm으로 관리할 수 없다(`/Library/LaunchAgents`는 탐색되지만 읽기 전용). 수동 추적 지정은 탐색됐지만 AI로 분류되지 않은 항목에만 쓸 수 있다 |
| `StandardOutPath`·`StandardErrorPath`를 지정한다 | 로그 보기와 마지막 실행 시각 추정이 안 된다. 상태와 종료 코드는 보인다 |
| `claude`·`codex`를 절대경로로 쓰거나 PATH를 지정한다 | 표시는 되지만 실행이 실패할 수 있어서 경고를 띄운다 |
| 비대화형 권한 옵션을 넣는다 | 표시와 실행은 되지만 루틴이 권한 확인에서 멈출 수 있다 |
| Label을 일관된 형식(`com.<이름>.<루틴명>`)으로 짓는다 | 영향 없음. 목록에서 찾기만 불편하다 |
| 스크립트로 감쌀 때 스크립트 안에서 `claude`·`codex`를 직접 호출한다 | 자동 분류에서 빠질 수 있다. 수동 추적 지정으로 보완한다 |

- 스킬 작성은 rtm 일정(14장)과 별개로 진행한다. rtm M3(수동 실행) 이후에 만들면 4번 확인 단계를 rtm으로 할 수 있다
