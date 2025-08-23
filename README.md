# lamamp.is
Repository for my personal website.

Check it out at www.lamamp.is

Build with ```go build -o lamamp```

Run with ```./lamamp --prod```

Or just ```go run main.go --prod```

To test locally remove the ```--prod``` flag

To run with [Air:](https://github.com/air-verse/air) (auto reload):
add ```alias air='~/go/bin/air'``` to .zshrc/.bashrc

```air```

```air -- --prod```


# Update
Update with ```git pull origin main```

Auto update with crontab:
```crontab -e```
```* * * * * cd lamamp.is && git pull origin main > /dev/null 2>&1```
