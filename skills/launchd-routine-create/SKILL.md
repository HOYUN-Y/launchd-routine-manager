---
name: launchd-routine-create
description: >-
  맥에서 Claude Code·Codex의 스킬이나 프롬프트, 스크립트를 정해진 시각에
  자동 실행하는 루틴을 launchd LaunchAgent로 만들어 등록한다. 실행 스크립트,
  plist 작성, 검증, 등록, 시험 실행과 결과 확인까지 한다. "매일 9시에 이 스킬
  돌려줘", "루틴 만들어줘", "정기 실행 등록", "launchd로 예약" 같은 요청에
  사용한다. 이미 등록된 루틴의 조회·수동 실행·일정 변경은 rtm으로 하고
  이 스킬로 하지 않는다.
---

# launchd 루틴 만들기

등록된 루틴은 rtm(`rtm list`, `rtm show`, `rtm run`, `rtm schedule`)으로
조회·실행·일정 변경한다. 이 스킬은 루틴을 새로 만드는 일만 한다.

아래 "권장 사항"은 필수가 아니다. 루틴마다 지킬 수 없는 항목이 있다.
지킬 수 있는 것만 적용하고, 못 지킨 항목과 그 영향은 끝에 사용자에게 알린다.

## 1. 정할 것

사용자와 아래를 정한다. 요청에 이미 있으면 다시 묻지 않는다.

| 항목 | 예 |
|---|---|
| 무엇을 실행하나 | 스킬 `/changelog-update`, 자유 프롬프트, 기존 스크립트 |
| 어떤 에이전트 | `claude` 또는 `codex` |
| 어디서 실행하나(작업 폴더) | `/Users/<me>/Documents/GitHub/blog-automation` |
| 언제 | 매일 09:00, 평일 18:00, 30분마다 |
| 루틴 이름 | `changelog-update` (Label과 파일 이름에 쓴다) |
| 바깥에 영향을 주는가 | 글 게시, 메일 발송, push 등 |

바깥에 영향을 주는 루틴이면 시험 실행(5단계) 전에 사용자 확인을 받는다.

## 2. 실행 파일 위치 확인

launchd는 로그인 셸의 PATH를 쓰지 않는다. 이 맥의 실제 경로를 확인한다.

```bash
zsh -lc 'command -v claude; command -v codex'
claude --help    # 설치된 버전의 플래그 확인
codex exec --help
```

### 충돌 확인 (파일을 만들기 전에)

스크립트와 plist를 쓰기 전에 아래 세 가지를 한꺼번에 확인한다. 하나라도 이미 있으면
멈추고 사용자에게 묻는다. 기존 파일이나 심볼릭 링크를 사용자 확인 없이 교체하지 않는다.
이미 등록된 루틴의 스크립트를 바꾸면 그 루틴의 동작이 바뀐다.

```bash
launchctl print gui/$(id -u)/<Label> >/dev/null 2>&1 && echo "Label 이미 로드됨"
ls -l ~/Library/LaunchAgents/<Label>.plist             # 있으면 충돌
ls -l ~/.local/share/routines/<이름>/run.sh            # 있으면 충돌
```

## 3. 실행 스크립트 작성

권장 위치: `~/.local/share/routines/<이름>/run.sh` (권한 `chmod +x`)

```bash
#!/bin/zsh
set -euo pipefail
cd "/작업/폴더"
echo "=== start $(date '+%F %T')"

# Claude: 비대화형이라 권한 확인에서 멈추지 않도록 필요한 도구만 허용한다
/절대/경로/claude -p "/changelog-update" \
  --allowedTools "Read" "Edit" "Bash(git status:*)"

# Codex 예:
# /절대/경로/codex exec -s workspace-write "프롬프트"
# 작업 폴더가 Git 저장소가 아니면 codex exec가 거부한다. 그 경우에만
# --skip-git-repo-check를 추가한다(sandbox 설정은 그대로 둔다).
# 확인: git -C "/작업/폴더" rev-parse --is-inside-work-tree

echo "=== end $(date '+%F %T')"
```

- `--allowedTools`는 루틴이 실제로 쓰는 도구만 넣는다. 범위를 넓히는 옵션(전체 권한 우회 등)은 사용자가 명시적으로 원할 때만 쓴다
- 비밀 값(API 키 등)을 스크립트나 plist에 직접 쓰지 않는다. 이미 에이전트 로그인으로 인증돼 있으면 추가 설정이 필요 없다

## 4. plist 작성

권장 위치: `~/Library/LaunchAgents/<Label>.plist`
권장 Label 형식: `com.<사용자이름>.routine.<이름>`

같은 Label이나 같은 파일이 이미 있으면 덮어쓰지 말고 사용자에게 묻는다.

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.me.routine.changelog-update</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/me/.local/share/routines/changelog-update/run.sh</string>
  </array>
  <key>StartCalendarInterval</key>
  <dict><key>Hour</key><integer>9</integer><key>Minute</key><integer>0</integer></dict>
  <key>StandardOutPath</key><string>/Users/me/Library/Logs/routines/changelog-update.out.log</string>
  <key>StandardErrorPath</key><string>/Users/me/Library/Logs/routines/changelog-update.err.log</string>
  <key>EnvironmentVariables</key>
  <dict><key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/Users/me/.local/bin</string></dict>
</dict>
</plist>
```

일정 형식:

- 매일 HH:MM: `StartCalendarInterval`에 `Hour`, `Minute`
- 요일 지정: `Weekday` 추가(0 또는 7 = 일요일, 1 = 월요일). 여러 요일이면 dict 배열
- 하루 여러 번: `StartCalendarInterval`을 dict 배열로
- N초마다: `StartInterval`(초 단위 정수)

로그 폴더는 미리 만든다: `mkdir -p ~/Library/Logs/routines`

## 5. 검증, 등록, 시험 실행

```bash
plutil -lint ~/Library/LaunchAgents/<Label>.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/<Label>.plist

# 시험 실행 (바깥에 영향을 주는 루틴이면 사용자 확인 후)
rtm run <Label>                                   # rtm이 있을 때
launchctl kickstart gui/$(id -u)/<Label>          # rtm이 없을 때

# 결과 확인
rtm show <Label>                                  # 또는:
launchctl print gui/$(id -u)/<Label> | grep -E 'state|last exit code'
tail -n 30 ~/Library/Logs/routines/<이름>.out.log ~/Library/Logs/routines/<이름>.err.log
```

종료 코드가 0이 아니면 err 로그를 읽고 고친다. 흔한 원인:

- `command not found`: 2단계 절대경로 또는 plist의 PATH 확인
- 권한 확인에서 진행 못 함: `--allowedTools`에 필요한 도구 추가
- 작업 폴더 문제: 스크립트의 `cd` 경로 확인

## 6. 권장 사항과 지키지 않았을 때의 영향

| 권장 사항 | 지키지 않으면 |
|---|---|
| plist를 `~/Library/LaunchAgents`에 둔다 | rtm 탐색에서 빠져 rtm으로 조회·실행·일정 변경을 할 수 없다. 사용자에게 알린다 |
| `StandardOutPath`·`StandardErrorPath` 지정 | rtm에서 로그를 볼 수 없다. 상태·종료 코드는 보인다 |
| `claude`·`codex` 절대경로 또는 PATH 지정 | 예약 실행이 `command not found`로 실패할 수 있다 |
| 비대화형 권한 옵션 지정 | 루틴이 권한 확인에서 멈추거나 실패한다 |
| Label을 일관된 형식으로 | 기능 영향 없음. 목록에서 찾기 불편하다 |
| 스크립트 안에서 `claude`·`codex`를 직접 호출 | rtm 자동 분류에서 빠질 수 있다. `rtm track`으로 보완 |

## 7. 마무리 보고

사용자에게 아래를 알린다.

- Label, plist 경로, 스크립트 경로, 로그 경로
- 일정과 다음 실행 예정 시각
- 시험 실행 결과(종료 코드, 로그 요약). 시험 실행을 건너뛰었으면 그 이유
- 지키지 못한 권장 사항과 그 영향(6장 표 기준)
- 이후 관리 방법: `rtm show <Label>`, `rtm run <Label>`, `rtm schedule <Label> --daily HH:MM`

## 하지 않는 것

- `/Library/LaunchAgents`, `/Library/LaunchDaemons`에 만들지 않는다
- 기존 plist를 사용자 확인 없이 덮어쓰거나 지우지 않는다
- 사용자 확인 없이 바깥에 영향을 주는 루틴을 시험 실행하지 않는다
