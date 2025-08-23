# lamamp.is
Repository for my personal website.

Check it out at www.lamamp.is

## Run
Locally: ```go run main.go```

With HTTPS: ```go run main.go --prod```

### To run with [Air:](https://github.com/air-verse/air) (auto reload):
add ```alias air='~/go/bin/air'``` to .zshrc/.bashrc

Locally: ```air```

with HTTPS: ```air -- --prod```

## Update
Update with ```git pull origin main```

Auto update with crontab:
```crontab -e```
```* * * * * cd lamamp.is && git pull origin main > /dev/null 2>&1```
