#!/bin/bash
# Pre-push CI simulation script
# Run this before pushing to catch common issues early

set -e

echo "=== Pre-push CI Check ==="
echo ""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

FAILED=0

# 1. Go checks
echo "1. Go build & test..."
if go build ./... 2>/dev/null && go test ./... -count=1 2>/dev/null | grep -q "ok"; then
    echo -e "   ${GREEN}✓${NC} Go build & test passed"
else
    echo -e "   ${RED}✗${NC} Go build or test failed"
    FAILED=1
fi

# 2. Go vet & fmt
echo "2. Go vet & fmt..."
if [ -z "$(gofmt -l .)" ] && go vet ./... 2>/dev/null; then
    echo -e "   ${GREEN}✓${NC} Go vet & fmt passed"
else
    echo -e "   ${RED}✗${NC} Go vet or fmt failed"
    FAILED=1
fi

# 3. Check gomobile dependency
echo "3. Gomobile dependency..."
if grep -q "golang.org/x/mobile" go.mod; then
    echo -e "   ${GREEN}✓${NC} gomobile dependency present"
else
    echo -e "   ${RED}✗${NC} gomobile dependency missing (run: go get -tool golang.org/x/mobile/cmd/gobind)"
    FAILED=1
fi

# 4. Flutter checks
echo "4. Flutter analyze..."
cd flutter_app
if /opt/flutter/bin/flutter analyze --no-fatal-infos 2>/dev/null | grep -q "No issues found"; then
    echo -e "   ${GREEN}✓${NC} Flutter analyze passed"
else
    echo -e "   ${RED}✗${NC} Flutter analyze failed"
    FAILED=1
fi
cd ..

# 5. Check Windows platform files
echo "5. Windows platform files..."
if [ -d "flutter_app/windows" ] && [ -f "flutter_app/windows/CMakeLists.txt" ]; then
    echo -e "   ${GREEN}✓${NC} Windows platform files present"
else
    echo -e "   ${RED}✗${NC} Windows platform files missing (run: flutter create --platforms=windows .)"
    FAILED=1
fi

# 6. Check Android platform files
echo "6. Android platform files..."
if [ -d "flutter_app/android" ] && [ -f "flutter_app/android/app/build.gradle" ]; then
    echo -e "   ${GREEN}✓${NC} Android platform files present"
else
    echo -e "   ${RED}✗${NC} Android platform files missing"
    FAILED=1
fi

# 7. Check Android libs directory
echo "7. Android libs directory..."
if [ -d "flutter_app/android/app/libs" ]; then
    echo -e "   ${GREEN}✓${NC} Android libs directory exists"
else
    echo -e "   ${RED}✗${NC} Android libs directory missing (will be created in CI)"
    # Not a failure, CI will create it
fi

echo ""
if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}All checks passed!${NC} Ready to push."
    exit 0
else
    echo -e "${RED}Some checks failed.${NC} Please fix before pushing."
    exit 1
fi
