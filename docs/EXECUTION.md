# 실행 계획 (1차: MVP M1~M5)

작성일: 2026-11-12
기준 문서: [PLAN.md](PLAN.md)

이 문서는 세 세션이 같은 기준으로 일하기 위한 작업 지시서다. 감독 세션은 진행할 때마다 맨 아래 진행 기록을 갱신한다. 컨텍스트가 압축돼도 이 문서로 상태를 복원한다.

## 1. 이번 차수 범위

- 포함: PLAN.md의 M1~M5(CLI `list`·`show`·`logs`·`run`·`schedule`·`doctor`, TUI), 루틴 생성 스킬 초안
- 제외: M6 배포(Homebrew tap, Releases), M7 이력 수집기·알림, 정밀 추적, 클라우드 루틴 조회. MVP가 끝난 뒤 다음 차수에서 다룬다

## 2. 세션 구성

모든 세션은 herdr 탭 `w1S:t1` 안에 둔다. 감독 페인은 `w1S:p1`이다. 다른 탭의 에이전트는 조작하지 않는다.

| 이름 | 역할 | 에이전트·모델 | cwd | 쓰기 권한 |
|---|---|---|---|---|
| 감독 (이 세션) | 지시, 검수 결과 판단, 병합, 스킬 작성, 진행 기록 | Claude Opus | `/Users/lyuhoyun/Documents/GitHub/launchd-routine-manager` (main) | `docs/`, `skills/` |
| `rtm-impl` | Go 구현 | Claude Sonnet | `/Users/lyuhoyun/Documents/GitHub/launchd-routine-manager-impl` (worktree, 브랜치 `impl/rtm`) | 위 worktree 안의 `docs/`·`skills/` 외 전부 |
| `rtm-review` | 마일스톤별 독립 검수 | Codex `gpt-6.1-sol` medium | 위 worktree | 읽기 전용 (sandbox `read-only`) |

- 메시지 첫 줄에는 `[Herdr: 감독 → rtm-impl]` 형식의 표식을 붙인다. 회신은 `[Herdr: rtm-impl → 감독]`
- 작업자는 자기 대화에 회신을 남기고, 감독이 `herdr agent read`로 읽는다. 작업자끼리 직접 메시지를 보내지 않는다

## 3. 브랜치와 병합

- `main`: 감독이 `docs/`, `skills/`를 쓴다
- `impl/rtm`: 구현 세션이 worktree에서 쓴다. 마일스톤마다 커밋 하나 이상, 메시지는 `M1: …` 형식
- 검수를 통과한 마일스톤은 감독이 `main`에 병합한다(`git merge --no-ff impl/rtm`). 두 브랜치가 쓰는 폴더가 겹치지 않아서 충돌이 나지 않아야 한다
- 원격 push는 사용자가 요청할 때만 한다

## 4. 구현 기준

### 모듈과 의존성

- 모듈 경로: `github.com/HOYUN-Y/launchd-routine-manager`, 바이너리 이름 `rtm`
- Go 1.27(설치됨: `go1.27.4 darwin/arm64`)
- 허용 의존성: `howett.net/plist`, M5에서 `github.com/charmbracelet/bubbletea`·`bubbles`·`lipgloss`. 그 외 추가는 감독 승인 후
- CLI 서브커맨드 파싱은 표준 `flag`로 한다

### 패키지 구성 (권장, 구현 중 합리적인 변경 허용)

```
cmd/rtm/main.go          서브커맨드 분기, TUI 진입
internal/sys             uid·홈·실행 파일 탐색, 외부 명령 실행 인터페이스(테스트에서 가짜로 교체)
internal/launchd         plist 스캔·읽기·쓰기, launchctl print 파싱, kickstart·bootstrap·bootout
internal/cron            crontab -l 파싱
internal/classify        AI 루틴 분류와 근거
internal/schedule        일정 해석·표시, 다음 실행 시각 계산, 일정 편집
internal/tui             M5
testdata/                plist·launchctl print 출력·crontab 샘플
```

- 외부 명령(`launchctl`, `plutil`, `crontab`, `zsh -lc`)은 모두 `internal/sys`의 한 인터페이스를 거친다. 단위 테스트는 `testdata/`의 실제 출력 샘플로 가짜 실행기를 만든다
- `--json` 출력 형식은 `internal`의 구조체를 그대로 직렬화한다. 필드 이름은 snake_case

### 안전 규칙 (개발·테스트 중 반드시 지킨다)

1. 상태를 바꾸는 명령(`kickstart`, `bootstrap`, `bootout`, plist 쓰기)은 Label이 `dev.rtm.test.`로 시작하는 테스트 루틴에만 실행한다
2. 테스트 plist는 `~/Library/LaunchAgents/dev.rtm.test.*.plist`로 만들고, 테스트가 끝나면 `bootout`하고 파일을 지운다
3. 실제 루틴(`ai.hermes.gateway` 등)에는 읽기 명령(`launchctl print`, plist 읽기)만 쓴다
4. 실제 launchd를 건드리는 통합 테스트는 `RTM_INTEGRATION=1`일 때만 돈다. 기본 `go test ./...`는 실제 launchd를 건드리지 않는다
5. `/Library` 아래 파일은 쓰지 않는다

## 5. 마일스톤별 완료 기준

각 마일스톤이 끝나면 구현 세션은 `go vet ./...`, `go test ./...`를 통과시키고 커밋한 뒤 보고한다.

### M1: 탐색·분류·`rtm list`

- `~/Library/LaunchAgents`, `/Library/LaunchAgents`의 plist와 `crontab -l`을 읽는다. 읽을 수 없는 파일은 건너뛰고 경고만 남긴다
- 분류 규칙은 PLAN.md 3장. 결과에 근거(파일 경로, 줄)를 포함한다
- 수동 추적 목록: `~/.config/rtm/config.json`의 `{"tracked": ["label", …]}`(파싱 실패 시 쓰기 거부, 모르는 키 보존). crontab 항목의 식별자는 줄 번호가 아니라 일정+명령의 해시(`cron:<8자리>`). `rtm track`/`untrack`
- `rtm list`는 기본 AI 루틴만, `--all`은 전체. `--json` 지원
- 테스트: `testdata/`의 plist 3종(claude 직접 호출, 셸 스크립트 래핑, 무관한 항목)이 각각 AI·AI(근거: 스크립트 n번째 줄)·비AI로 분류된다. crontab 샘플 2줄(claude 포함/미포함)도 같은 방식

### M2: 상태·로그·다음 실행 시각, `rtm show`·`rtm logs`

- `launchctl print gui/<uid>/<label>`에서 state, pid, runs, last exit code, stdout·stderr 경로를 파싱한다. 실패하면 "로드 안 됨"
- 다음 실행 시각: `StartCalendarInterval`(단일·배열, Minute/Hour/Weekday/Day/Month 조합), `StartInterval`. `RunAtLoad`·`KeepAlive`만 있으면 "상시" 또는 "로그인 시"
- `rtm logs <label> [-n 50] [-f]`: 로그 경로가 없으면 그 사실을 알린다
- 테스트: 다음 실행 시각 계산을 기준 시각 고정으로 단위 테스트(매일, 요일, 배열, 월말 경계, Weekday 0과 7). `launchctl print` 출력 샘플 3종(running, 종료됨 exit 1, 미로드) 파싱
- 실기 확인: `rtm list`, `rtm show ai.hermes.gateway`가 실제 상태(running, pid)를 보여준다

### M3: 수동 실행 `rtm run`

- PLAN.md 5장 동작. 실행 중이면 `--restart` 없이는 거부, KeepAlive 항목은 `--yes` 없이는 재시작 거부(비대화형 CLI이므로 확인은 플래그로)
- 로드 안 된 루틴은 `--load`가 있을 때만 bootstrap 후 실행
- 실행 후 최대 10초간 상태를 다시 읽어 결과를 출력한다
- 통합 테스트(`RTM_INTEGRATION=1`): `dev.rtm.test.ok`(`exit 0`), `dev.rtm.test.fail`(`exit 1`)을 만들고 `rtm run` 후 last exit code가 각각 0, 1로 보이고 로그가 갱신되는지 확인, 정리까지

### M4: 일정 변경 `rtm schedule`

- PLAN.md 6장 동작. 플래그: `--daily HH:MM`, `--weekly mon,wed HH:MM`, `--at HH:MM,HH:MM`, `--every 30m`, `--revert`
- `~/Library/LaunchAgents` 밖의 plist는 거부. 표현 불가 일정은 덮어쓰기 전 `--force` 요구
- 백업 위치 `~/.local/state/rtm/backups/<label>-<YYYYMMDD-HHMMSS>.plist`
- 테스트: 일정 키 외 모든 키가 쓰기 전후 동일한지(단위), `plutil -lint` 실패 시 원복(가짜 실행기), 통합 테스트에서 `dev.rtm.test.sched`의 일정 변경 → 재로드 → `launchctl print`에 반영 → `--revert`로 원복

### M5: TUI

- 인자 없이 `rtm` 실행 시 TUI. PLAN.md 4장의 목록·키(enter, r, e, l, t, /, a, q)
- TUI는 CLI와 같은 internal 함수를 쓴다. TUI 전용 로직은 화면과 입력 처리만
- 역할 분담: 기능(모델·키 처리·화면 전환·데이터 연결)은 `rtm-impl`, 미감(색·테두리·여백·상태 배지·빈 화면·도움말 줄)은 감독이 맡는다
  - `rtm-impl`은 모든 lipgloss 스타일을 `internal/tui/style.go` 한 파일에 모으고, 화면 코드는 그 파일의 스타일만 참조한다. 기본 스타일만 두고 꾸미지 않는다
  - M5 기능 커밋 뒤 `rtm-impl`이 idle일 때 감독이 worktree에서 `internal/tui/style.go`(필요하면 화면 코드의 배치 부분)를 다듬어 `M5-style: …`으로 커밋한다. 두 세션이 동시에 같은 파일을 고치지 않는다
- 위험 동작(재시작, 일정 변경 재로드)은 확인 대화상자
- 확인: 실제 맥에서 목록 표시, 테스트 루틴 실행·일정 변경을 TUI로 수행하고 결과 화면을 보고에 첨부(텍스트 캡처)

### `rtm doctor` (M2와 함께)

- uid·홈, `claude`·`codex` 경로(로그인 셸 탐색 결과), `~/Library/LaunchAgents` 읽기·쓰기 가능 여부, launchctl 접근 여부를 출력

## 6. 검수 절차

1. 구현 세션이 마일스톤 커밋 후 보고한다(커밋 해시, 변경 요약, 테스트 결과)
2. 감독이 `rtm-review`에 검수를 지시한다: 대상 커밋 범위, PLAN.md·이 문서의 해당 완료 기준, 안전 규칙
3. 검수 세션은 파일을 수정하지 않고 지적 사항을 심각도(P0 데이터 손상·안전 규칙 위반 / P1 기능 오류 / P2 개선)와 `파일:줄`, 재현 방법으로 보고한다
4. 감독이 지적 사항을 판단해 받아들일 것만 구현 세션에 전달한다
5. 수정 후 해당 지적 사항만 재검수한다. P0·P1이 남아 있지 않으면 감독이 `main`에 병합한다

스킬 초안도 M3 이후 같은 절차로 검수한다.

## 7. 승인 처리

사용자가 승인 처리를 감독 세션에 위임했다.

- `rtm-impl`은 `--permission-mode acceptEdits`로 시작하고 `go`, `gofmt`, `git`, `plutil`, `ls`, `cat`, `mkdir`, `rm -f ~/Library/LaunchAgents/dev.rtm.test.*` 등 일반 개발 명령을 미리 허용한다
- 그 외 승인 요청(주로 `launchctl`)은 감독이 화면을 읽고 안전 규칙 1~5에 맞을 때만 승인한다. 테스트 Label이 아니면 거부하고 이유를 전달한다
- `rtm-review`는 읽기 전용 sandbox라 쓰기 승인이 생기지 않는다. 테스트 실행이 막히면 감독이 대신 실행해 결과를 전달한다

## 8. 컨텍스트 관리

- Claude 세션(`rtm-impl`)의 컨텍스트 사용률은 상태줄의 `ctx NN%`로 확인한다
- 감독은 마일스톤 보고를 받을 때마다 확인한다. 65% 이상이면 하던 작업이 끝난(idle) 뒤 `/compact`를 보낸다. 압축 지시에는 현재 마일스톤, 남은 지적 사항, 이 문서 경로를 남기도록 적는다
- 작업 도중(working)에는 압축을 보내지 않는다
- 감독 세션 자체는 하네스가 자동으로 압축한다. 압축에 대비해 진행 상태는 아래 진행 기록에 남긴다

## 9. 스킬 작업 (감독 담당)

- 위치: `skills/launchd-routine-create/SKILL.md` (main 브랜치)
- 내용: PLAN.md 16장. 권장 사항은 필수가 아니며, 못 지킨 항목은 사용자에게 알린다
- 설치: 기존 스킬과 같은 방식(`~/.claude/skills`, `~/.codex/skills`에 심볼릭 링크)으로 하되, 사용자 확인 후 진행한다

## 10. 무인 진행 원칙

사용자가 자리를 비운 동안 감독은 멈추지 않고 M1~M5와 스킬 초안까지 진행한다.

- 사용자에게 물어야 할 결정이 생기면 PLAN.md·이 문서 기준으로 보수적인 쪽(데이터를 덜 건드리는 쪽, 범위를 덜 넓히는 쪽)을 택하고, 결정 내용을 진행 기록에 남긴다
- 원격 push, 스킬 설치(홈 폴더에 링크), 실제 루틴 변경처럼 되돌리기 어렵거나 바깥에 영향을 주는 일은 하지 않고 최종 보고에 "사용자 확인 필요"로 남긴다
- 작업자가 같은 오류로 3번 이상 막히면 감독이 원인을 직접 조사하고, 해결이 안 되면 해당 항목을 보류로 기록하고 다음 마일스톤으로 넘어간다
- 끝나면 진행 기록에 최종 결과, 남은 지적 사항, 사용자 확인 필요 항목을 정리한다

## 11. 진행 기록

| 시각 | 내용 |
|---|---|
| 2026-11-12 | Go 1.27.4 설치, 실행 계획 작성. 사용자 지시: 무인으로 끝까지 진행, TUI 미감은 감독 담당(`insight-design-theme` 스킬 참고) |
| 2026-11-12 | 세션 시작: `rtm-impl` = `w1S:p2`(Sonnet 5.5), `rtm-review` = `w1S:p3`(GPT-6.1-Sol medium). 두 폴더 신뢰 승인 |
| 2026-11-12 | 결정: 무인 진행 중 승인 대기로 멈추지 않도록 `rtm-impl`을 auto mode로 전환. 안전 규칙은 지시문과 검수로 확인 |
| 2026-11-12 | 스킬 초안 `skills/launchd-routine-create/SKILL.md` 커밋(7234d1d). plist 예시 `plutil -lint` 통과 |
| 2026-11-12 | M1 커밋 7be1818. 감독 확인: vet/test 통과, 실제 맥 `rtm list --all` 동작. "no Label" 경고 4건은 실제 빈 plist라 정상. ctx 14%. Codex 검수 지시, M2 지시 |
| 2026-11-12 | M1 검수(Codex): P0 config 정규식 손상, P1 따옴표 경로 분류 누락, P1 cron 줄번호 식별자, P2 직접 호출 근거. 감독 판단: 전부 수용. 결정: 설정을 config.json(표준 라이브러리)으로 변경, cron 식별자를 해시로. M2 완료 후 수정 지시 예정 |
